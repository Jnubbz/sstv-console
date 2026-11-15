package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
	"time"
)

func solid(c color.RGBA, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func TestPhotoLibrary(t *testing.T) {
	dir := t.TempDir()
	red, blue := solid(color.RGBA{R: 255}, 400, 300), solid(color.RGBA{B: 255}, 400, 300)
	a, err := addPhoto(dir, red)
	if err != nil {
		t.Fatal(err)
	}
	// the same picture again is the same entry
	if a2, _ := addPhoto(dir, red); a2 != a || len(listPhotos(dir)) != 1 {
		t.Fatalf("duplicate made a new entry: %v", listPhotos(dir))
	}
	os.Chtimes(photoFile(dir, a, ""), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	b, err := addPhoto(dir, blue)
	if err != nil || b == a {
		t.Fatal("second photo", err)
	}
	if l := listPhotos(dir); len(l) != 2 || l[0].ID != b {
		t.Fatalf("most recently used first: %v", l)
	}
	if th, err := os.ReadFile(photoFile(dir, a, ".thumb")); err != nil || len(th) == 0 {
		t.Fatal("no thumbnail")
	}
	// picking the first again: it becomes the current photo and comes back to the front, with its framing
	savePhotoView(dir, a, `{"zoom":2}`)
	v, err := usePhoto(dir, a)
	if err != nil || v != `{"zoom":2}` {
		t.Fatalf("use: %q %v", v, err)
	}
	if cur, _ := os.ReadFile(photoPath(dir)); !bytes.Equal(cur, red) {
		t.Fatal("current photo not switched")
	}
	if l := listPhotos(dir); l[0].ID != a {
		t.Fatalf("used photo should lead: %v", l)
	}
	// only ids the library made are accepted
	for _, bad := range []string{"../settings.json", "", "ZZZZZZZZZZZZZZZZ", "0123456789abcdef"} {
		if _, err := usePhoto(dir, bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := addPhoto(dir, []byte("not a picture")); err == nil {
		t.Fatal("accepted a non-picture")
	}
}

func TestPhotoLibraryKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxLibrary+5; i++ {
		if _, err := addPhoto(dir, solid(color.RGBA{R: uint8(i), G: uint8(i * 3)}, 8+i, 8)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(listPhotos(dir)); n != maxLibrary {
		t.Fatalf("%d kept, want %d", n, maxLibrary)
	}
}
