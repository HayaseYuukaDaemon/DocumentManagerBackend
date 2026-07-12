package jmcomic

import (
	"encoding/json"
	"testing"
)

func TestAlbumUnmarshalJSONPreservesRaw(t *testing.T) {
	raw := []byte(`{"id":1430149,"name":"album","series":[]}`)
	album := Album{}
	if err := json.Unmarshal(raw, &album); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if string(album.ID) != "1430149" || album.Name != "album" {
		t.Fatalf("unexpected album: %#v", album)
	}
	if string(album.Raw) != string(raw) {
		t.Fatalf("raw JSON was not preserved: %s", album.Raw)
	}
}

func TestPhotoUnmarshalJSONPreservesRaw(t *testing.T) {
	raw := []byte(`{"id":"1430149","name":"photo","images":["00001.webp"]}`)
	photo := Photo{}
	if err := json.Unmarshal(raw, &photo); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if string(photo.ID) != "1430149" || len(photo.Images) != 1 {
		t.Fatalf("unexpected photo: %#v", photo)
	}
	if string(photo.Raw) != string(raw) {
		t.Fatalf("raw JSON was not preserved: %s", photo.Raw)
	}
}
