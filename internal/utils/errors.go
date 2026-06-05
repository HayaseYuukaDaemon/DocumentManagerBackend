package utils

import "fmt"

type IndexedError struct {
	Err   error
	Index int
}

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
