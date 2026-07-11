package archive

import "testing"

func TestPageObjectKeyUsesHash(t *testing.T) {
	key, err := PageObjectKey("42", "abcdef123456")
	if err != nil {
		t.Fatalf("PageObjectKey returned error: %v", err)
	}
	if key != "documents/42/pages/abcdef123456" {
		t.Fatalf("unexpected page object key: %s", key)
	}
}

func TestPageObjectKeyRejectsUnsafeHash(t *testing.T) {
	for _, hash := range []string{"", ".", "..", "nested/hash", `nested\hash`} {
		t.Run(hash, func(t *testing.T) {
			if _, err := PageObjectKey("42", hash); err == nil {
				t.Fatalf("expected hash %q to be rejected", hash)
			}
		})
	}
}

func TestDocumentObjectPrefixDoesNotMatchSiblingDocument(t *testing.T) {
	if got := DocumentObjectPrefix("1"); got != "documents/1/" {
		t.Fatalf("unexpected document prefix: %s", got)
	}
}
