package sources

import "strconv"

type SourceType string

type ErrPageHashMismatch struct {
	DocumentID int
	PageIndex  int
	Expected   string
	Actual     string
}

func (e ErrPageHashMismatch) Error() string {
	return "page hash mismatch at index " + strconv.Itoa(e.PageIndex) + ": expected " + e.Expected + ", got " + e.Actual
}
