package archive

import "testing"

func TestPageObjectKeyUsesHash(t *testing.T) {
	key, err := PageObjectKey("42", "abcdef123456", "image/webp")
	if err != nil {
		t.Fatalf("PageObjectKey returned error: %v", err)
	}
	if key != "documents/42/pages/abcdef123456.webp" {
		t.Fatalf("unexpected page object key: %s", key)
	}
}

func TestPageObjectKeyUsesContentTypeExtension(t *testing.T) {
	tests := map[string]string{
		"image/webp": "webp",
		"image/jpeg": "jpg",
		"image/png":  "png",
		"image/avif": "avif",
		"text/plain": "bin",
	}
	for contentType, ext := range tests {
		t.Run(contentType, func(t *testing.T) {
			key, err := PageObjectKey("42", "hash", contentType)
			if err != nil {
				t.Fatalf("PageObjectKey returned error: %v", err)
			}
			if key != "documents/42/pages/hash."+ext {
				t.Fatalf("unexpected page object key: %s", key)
			}
		})
	}
}

func TestPageObjectKeyRejectsUnsafeHash(t *testing.T) {
	for _, hash := range []string{"", ".", "..", "nested/hash", `nested\hash`} {
		t.Run(hash, func(t *testing.T) {
			if _, err := PageObjectKey("42", hash, "image/webp"); err == nil {
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
