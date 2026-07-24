package sources

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	defaultSchedulerInitialBackoff  = time.Second
	defaultSchedulerMaxBackoff      = 30 * time.Second
	defaultSchedulerMaxTaskAttempts = 5
)

// ErrRateLimited marks an error as a shared rate-limit signal for a scheduler.
var ErrRateLimited = errors.New("source rate limited")

// RateLimitError optionally supplies a server-requested delay for a rate-limit signal.
type RateLimitError interface {
	error
	RetryAfterDuration() time.Duration
}

// ConcurrencyScheduler applies adaptive concurrency and shared rate-limit backoff.
type ConcurrencyScheduler struct {
	sem chan struct{}

	mu                 sync.Mutex
	changed            chan struct{}
	limit              int
	successes          int
	generation         uint64
	blockedUntil       time.Time
	backoff            time.Duration
	initialBackoff     time.Duration
	maxBackoff         time.Duration
	maxAttemptsPerTask int
}

type schedulerPermit struct {
	generation uint64
}

// NewConcurrencyScheduler creates a scheduler with adaptive concurrency between the given bounds.
func NewConcurrencyScheduler(initialConcurrency, maxConcurrency int) *ConcurrencyScheduler {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	if initialConcurrency < 1 {
		initialConcurrency = 1
	}
	if initialConcurrency > maxConcurrency {
		initialConcurrency = maxConcurrency
	}
	return &ConcurrencyScheduler{
		sem:                make(chan struct{}, maxConcurrency),
		changed:            make(chan struct{}),
		limit:              initialConcurrency,
		initialBackoff:     defaultSchedulerInitialBackoff,
		maxBackoff:         defaultSchedulerMaxBackoff,
		maxAttemptsPerTask: defaultSchedulerMaxTaskAttempts,
	}
}

// Do runs a task when capacity is available and retries shared rate-limit failures.
func (s *ConcurrencyScheduler) Do(ctx context.Context, task func(context.Context) error) error {
	for attempt := 1; ; attempt++ {
		permit, err := s.acquire(ctx)
		if err != nil {
			return err
		}

		err = task(ctx)
		s.finish(permit, err)
		if !errors.Is(err, ErrRateLimited) {
			return err
		}
		if attempt >= s.maxAttemptsPerTask {
			return fmt.Errorf("source request remained rate limited after %d attempts: %w", attempt, err)
		}
	}
}

func (s *ConcurrencyScheduler) acquire(ctx context.Context) (schedulerPermit, error) {
	for {
		s.mu.Lock()
		wait := time.Until(s.blockedUntil)
		if wait <= 0 && len(s.sem) < s.limit {
			s.sem <- struct{}{}
			permit := schedulerPermit{generation: s.generation}
			s.mu.Unlock()
			return permit, nil
		}
		changed := s.changed
		s.mu.Unlock()

		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return schedulerPermit{}, ctx.Err()
			case <-changed:
				timer.Stop()
			case <-timer.C:
			}
			continue
		}

		select {
		case <-ctx.Done():
			return schedulerPermit{}, ctx.Err()
		case <-changed:
		}
	}
}

func (s *ConcurrencyScheduler) finish(permit schedulerPermit, taskErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	<-s.sem
	if errors.Is(taskErr, ErrRateLimited) {
		s.applyRateLimitLocked(permit, taskErr)
	} else if taskErr == nil && permit.generation == s.generation {
		s.applySuccessLocked()
	}
	s.signalLocked()
}

func (s *ConcurrencyScheduler) applyRateLimitLocked(permit schedulerPermit, taskErr error) {
	// Requests from the same in-flight generation commonly fail together. Only the
	// first response changes the shared schedule; the rest observe the new generation.
	if permit.generation != s.generation {
		return
	}

	s.generation++
	s.limit = max(1, s.limit/2)
	s.successes = 0
	if s.backoff < s.initialBackoff {
		s.backoff = s.initialBackoff
	} else {
		s.backoff = min(s.backoff*2, s.maxBackoff)
	}

	var rateLimitErr RateLimitError
	if errors.As(taskErr, &rateLimitErr) {
		retryAfter := rateLimitErr.RetryAfterDuration()
		if retryAfter > s.backoff {
			s.backoff = min(retryAfter, s.maxBackoff)
		}
	}
	s.blockedUntil = time.Now().Add(s.backoff)
}

func (s *ConcurrencyScheduler) applySuccessLocked() {
	s.successes++
	if s.successes < s.limit {
		return
	}

	s.successes = 0
	if s.limit < cap(s.sem) {
		s.limit++
	}
	if s.backoff > s.initialBackoff {
		s.backoff /= 2
	} else {
		s.backoff = 0
	}
}

func (s *ConcurrencyScheduler) signalLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}
