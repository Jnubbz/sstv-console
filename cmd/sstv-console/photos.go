package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/Jnubbz/sstv-console/sstv"
)

// The photo library: every picture used to build a reply, kept so it can be picked again.
// Files are named by a hash of their bytes, so adding the same photo twice is one entry.
// Next to each: a small thumbnail (.thumb) and the framing last used with it (.view).

const maxLibrary = 40

var photoIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

func photosDir(dir string) string { return filepath.Join(dir, "photos") }

func photoFile(dir, id, ext string) string { return filepath.Join(photosDir(dir), id+ext) }

type photoInfo struct {
	ID string `json:"id"`
}

// addPhoto puts a picture in the library (or just refreshes it) and makes it the current photo.
func addPhoto(dir string, b []byte) (string, error) {
	if err := savePhoto(dir, b); err != nil { // checks it is a JPEG/PNG of sensible size
		return "", err
	}
	sum := sha256.Sum256(b)
	id := hex.EncodeToString(sum[:8])
	if err := os.MkdirAll(photosDir(dir), 0o755); err != nil {
		return "", err
	}
	if _, err := os.Stat(photoFile(dir, id, "")); err != nil {
		if err := writeFileAtomic(photoFile(dir, id, ""), b); err != nil {
			return "", err
		}
		if t := thumbnail(b); t != nil {
			writeFileAtomic(photoFile(dir, id, ".thumb"), t)
		}
	} else {
		now := time.Now()
		os.Chtimes(photoFile(dir, id, ""), now, now)
	}
	prunePhotos(dir)
	return id, nil
}

func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// thumbnail: a 160-pixel-wide JPEG, or nil if the picture can't be read.
func thumbnail(b []byte) []byte {
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w == 0 || h == 0 {
		return nil
	}
	tw, th := 160, 160*h/w
	if th < 1 {
		th = 1
	}
	var out bytes.Buffer
	if jpeg.Encode(&out, sstv.Resize(img, tw, th), &jpeg.Options{Quality: 80}) != nil {
		return nil
	}
	return out.Bytes()
}

// listPhotos: the library, most recently used first.
func listPhotos(dir string) []photoInfo {
	ents, _ := os.ReadDir(photosDir(dir))
	type e struct {
		id string
		t  time.Time
	}
	var all []e
	for _, f := range ents {
		if !photoIDRe.MatchString(f.Name()) {
			continue
		}
		if fi, err := f.Info(); err == nil {
			all = append(all, e{f.Name(), fi.ModTime()})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t.After(all[j].t) })
	out := make([]photoInfo, 0, len(all))
	for _, x := range all {
		out = append(out, photoInfo{x.id})
	}
	return out
}

// prunePhotos keeps the most recently used maxLibrary pictures.
func prunePhotos(dir string) {
	l := listPhotos(dir)
	for i := maxLibrary; i < len(l); i++ {
		for _, ext := range []string{"", ".thumb", ".view"} {
			os.Remove(photoFile(dir, l[i].ID, ext))
		}
	}
}

// usePhoto makes a library picture the current photo; it returns the framing last used with it.
func usePhoto(dir, id string) (view string, err error) {
	if !photoIDRe.MatchString(id) {
		return "", errors.New("unknown photo")
	}
	b, err := os.ReadFile(photoFile(dir, id, ""))
	if err != nil {
		return "", errors.New("that photo is no longer in the library")
	}
	if err := savePhoto(dir, b); err != nil {
		return "", err
	}
	now := time.Now()
	os.Chtimes(photoFile(dir, id, ""), now, now)
	v, _ := os.ReadFile(photoFile(dir, id, ".view"))
	return string(v), nil
}

func savePhotoView(dir, id, view string) {
	if photoIDRe.MatchString(id) {
		if _, err := os.Stat(photoFile(dir, id, "")); err == nil {
			writeFileAtomic(photoFile(dir, id, ".view"), []byte(view))
		}
	}
}
