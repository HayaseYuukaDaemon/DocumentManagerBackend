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

func (e IndexedError) Unwrap() error {
	return e.Err
}
