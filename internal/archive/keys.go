package archive

import (
	"errors"
	"path"
	"strings"
)

const objectRoot = "documents"

func DocumentObjectPrefix(documentID string) string {
	return path.Join(objectRoot, documentID) + "/"
}

func ManifestObjectKey(documentID string) string {
	return path.Join(DocumentObjectPrefix(documentID), "manifest.json")
}

func PageObjectKey(documentID string, hash string) (string, error) {
	documentID = strings.TrimSpace(documentID)
	hash = strings.TrimSpace(hash)
	if documentID == "" {
		return "", errors.New("document id is required")
	}
	if hash == "" {
		return "", errors.New("page hash is required")
	}
	if hash == "." || hash == ".." || strings.ContainsAny(hash, `/\`) {
		return "", errors.New("page hash must be a single safe path segment")
	}
	return path.Join(DocumentObjectPrefix(documentID), "pages", hash), nil
}
