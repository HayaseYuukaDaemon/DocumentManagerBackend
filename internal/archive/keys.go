package archive

import (
	"fmt"
	"path"
)

const objectRoot = "documents"

func ManifestObjectKey(documentID string) string {
	return path.Join(objectRoot, documentID, "manifest.json")
}

func PageObjectKey(documentID string, index int, contentType string) string {
	ext := contentTypeExt(contentType)
	return path.Join(objectRoot, documentID, "pages", fmt.Sprintf("%06d.%s", index+1, ext))
}

func contentTypeExt(contentType string) string {
	switch contentType {
	case "image/webp":
		return "webp"
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/avif":
		return "avif"
	default:
		return "bin"
	}
}
