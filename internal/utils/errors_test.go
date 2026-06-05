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
	if err.Index != 3 {
		t.Fatalf("unexpected index: %d", err.Index)
	}
}
