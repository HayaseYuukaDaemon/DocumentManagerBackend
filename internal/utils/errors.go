package utils

import (
	"errors"
	"fmt"
)

type IndexedError struct {
	Err   error
	Index int
}

var ErrNotImplemented = errors.New("This feature is not implemented")

func NewIndexedError(err error, index int) IndexedError {
	return IndexedError{
		Err:   err,
		Index: index,
	}
}

func (e IndexedError) Error() string {
	return fmt.Sprintf("err %v occur in %d", e.Err, e.Index)
}

func (e IndexedError) Is(target error) bool {
	typed, ok := target.(IndexedError)
	if !ok {
		return false
	}
	if typed.Err != nil && !errors.Is(e.Err, typed.Err) {
		return false
	}
	if typed.Index != 0 && e.Index != typed.Index {
		return false
	}
	return true
}

func (e IndexedError) Unwrap() error {
	return e.Err
}
