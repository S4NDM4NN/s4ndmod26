package main

import (
	"archive/zip"
	"bytes"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeZip(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, data := range files {
		w, _ := zw.Create(name)
		w.Write(data)
	}
	zw.Close()
	f.Close()
}

// 2x1 bottom-up 24-bit TGA: pixel (0,0) blue-ish in file order BGR.
func tga24() []byte {
	h := make([]byte, 18)
	h[2], h[12], h[14], h[16] = 2, 2, 1, 24
	return append(h, 10, 20, 30, 40, 50, 60) // B,G,R per pixel
}

func tgaRLE() []byte {
	h := make([]byte, 18)
	h[2], h[12], h[14], h[16] = 10, 2, 1, 24
	return append(h, 0x81, 1, 2, 3) // one packet repeating the pixel twice
}

func TestLevelshot(t *testing.T) {
	mod, main := t.TempDir(), t.TempDir()
	writeZip(t, filepath.Join(main, "pak0.pk3"), map[string][]byte{
		"levelshots/mp_base.tga":    tga24(),
		"levelshots/MP_Depot.JPG":   []byte("jpegdata-main"),
		"levelshots/rle.tga":        tgaRLE(),
		"levelshots/unknownmap.jpg": []byte("unknown"),
	})
	writeZip(t, filepath.Join(mod, "custom.pk3"), map[string][]byte{
		"levelshots/mp_depot.jpg": []byte("jpegdata-mod"),
		"levelshots/mycustom.jpg": []byte("custom"),
	})
	h := levelshotHandler(&levelshotStore{dirs: []string{mod, main}})
	get := func(p string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h(rr, httptest.NewRequest("GET", p, nil))
		return rr
	}

	if rr := get("/api/levelshot/mycustom"); rr.Code != 200 || rr.Body.String() != "custom" || rr.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("custom: %d %q %s", rr.Code, rr.Body.String(), rr.Header().Get("Content-Type"))
	}
	if rr := get("/api/levelshot/mp_depot"); rr.Body.String() != "jpegdata-mod" {
		t.Fatalf("mod pk3 should win over main: %q", rr.Body.String())
	}
	for _, name := range []string{"mp_base", "rle"} {
		rr := get("/api/levelshot/" + name)
		if rr.Code != 200 || rr.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("%s: %d %s", name, rr.Code, rr.Header().Get("Content-Type"))
		}
		img, err := png.Decode(bytes.NewReader(rr.Body.Bytes()))
		if err != nil || img.Bounds().Dx() != 2 || img.Bounds().Dy() != 1 {
			t.Fatalf("%s: bad png: %v", name, err)
		}
		if name == "mp_base" {
			r, g, b, _ := img.At(0, 0).RGBA()
			if r>>8 != 30 || g>>8 != 20 || b>>8 != 10 {
				t.Fatalf("channels swapped wrongly: %d %d %d", r>>8, g>>8, b>>8)
			}
		}
	}
	if rr := get("/api/levelshot/nosuchmap"); rr.Code != http.StatusNotFound {
		t.Fatalf("missing map: %d", rr.Code)
	}
	if rr := get("/api/levelshot/../../etc/passwd"); rr.Code != http.StatusNotFound {
		t.Fatalf("bad name: %d", rr.Code)
	}
	if rr := get("/api/levelshot/MP_DEPOT"); rr.Code != 200 {
		t.Fatalf("case-insensitive: %d", rr.Code)
	}
}
