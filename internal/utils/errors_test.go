package utils

import (
	"errors"
	"testing"
)

func TestIndexedError(t *testing.T) {
	baseErr := errors.New("download failed")
	err := NewIndexedError(baseErr, 3)

	if got := err.Error(); got != "err download failed occur in 3" {
		t.Fatalf("unexpected error string: %q", got)
	}
	if !errors.Is(err, baseErr) {
		t.Fatal("expected IndexedError to unwrap to base error")
	}
	if !errors.Is(err, IndexedError{}) {
		t.Fatal("expected IndexedError to match its own type via errors.Is")
	}
	if !errors.Is(err, IndexedError{Err: baseErr}) {
		t.Fatal("expected IndexedError to match IndexedError target with same base error")
	}
	if !errors.Is(err, IndexedError{Index: 3}) {
		t.Fatal("expected IndexedError to match IndexedError target with same index")
	}
	if errors.Is(err, IndexedError{Index: 2}) {
		t.Fatal("expected IndexedError not to match IndexedError target with different index")
	}
	if err.Index != 3 {
		t.Fatalf("unexpected index: %d", err.Index)
	}
}
