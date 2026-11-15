package main

// GET /api/levelshot/<map> returns the map's loading screen ("levelshot") from whichever pk3 the server
// has it in, so the MP4 export's title card can show it for any map the game has art for, not just the
// ones in the demo pak.  Levelshots are JPEG or TGA inside the pk3 (levelshots/<map>.jpg|.tga); TGA is
// converted to PNG for the browser.  404 means no pk3 has one.

import (
	"archive/zip"
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var levelshotNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Extensions in order of preference.
var levelshotExts = []string{".jpg", ".jpeg", ".png", ".tga"}

const (
	levelshotMaxBytes = 16 << 20
	levelshotHitTTL   = 10 * time.Minute
	levelshotMissTTL  = time.Minute
)

type levelshotEntry struct {
	data    []byte
	ctype   string
	found   bool
	expires time.Time
}

type levelshotStore struct {
	dirs  []string
	mu    sync.Mutex
	cache map[string]levelshotEntry
}

// pk3 files to search, most important first: earlier directories win, and within a directory the
// alphabetically later pk3 wins, the way the game itself resolves duplicate files.
func (s *levelshotStore) pk3Paths() []string {
	var out []string
	for _, dir := range s.dirs {
		names, err := filepath.Glob(filepath.Join(dir, "*.pk3"))
		if err != nil {
			continue
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		out = append(out, names...)
	}
	return out
}

func (s *levelshotStore) find(name string) (data []byte, ctype string, found bool) {
	wanted := map[string]bool{}
	for _, ext := range levelshotExts {
		wanted["levelshots/"+strings.ToLower(name)+ext] = true
	}
	for _, path := range s.pk3Paths() {
		zr, err := zip.OpenReader(path)
		if err != nil {
			continue
		}
		var best *zip.File
		bestRank := len(levelshotExts)
		for _, f := range zr.File {
			lower := strings.ToLower(f.Name)
			if !wanted[lower] {
				continue
			}
			for rank, ext := range levelshotExts {
				if strings.HasSuffix(lower, ext) && rank < bestRank {
					best, bestRank = f, rank
				}
			}
		}
		if best != nil && best.UncompressedSize64 <= levelshotMaxBytes {
			if rc, err := best.Open(); err == nil {
				raw, err := io.ReadAll(io.LimitReader(rc, levelshotMaxBytes))
				rc.Close()
				if err == nil {
					zr.Close()
					return encodeLevelshot(strings.ToLower(best.Name), raw)
				}
			}
		}
		zr.Close()
	}
	return nil, "", false
}

func encodeLevelshot(lowerName string, raw []byte) ([]byte, string, bool) {
	switch {
	case strings.HasSuffix(lowerName, ".jpg"), strings.HasSuffix(lowerName, ".jpeg"):
		return raw, "image/jpeg", true
	case strings.HasSuffix(lowerName, ".png"):
		return raw, "image/png", true
	case strings.HasSuffix(lowerName, ".tga"):
		img, err := decodeTGA(raw)
		if err != nil {
			return nil, "", false
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", false
		}
		return buf.Bytes(), "image/png", true
	}
	return nil, "", false
}

func (s *levelshotStore) get(name string) (levelshotEntry, bool) {
	key := strings.ToLower(name)
	s.mu.Lock()
	e, ok := s.cache[key]
	s.mu.Unlock()
	if ok && time.Now().Before(e.expires) {
		return e, e.found
	}
	data, ctype, found := s.find(name)
	e = levelshotEntry{data: data, ctype: ctype, found: found}
	if found {
		e.expires = time.Now().Add(levelshotHitTTL)
	} else {
		e.expires = time.Now().Add(levelshotMissTTL)
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]levelshotEntry{}
	}
	s.cache[key] = e
	s.mu.Unlock()
	return e, found
}

func levelshotHandler(s *levelshotStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/levelshot/")
		name = strings.TrimSuffix(strings.TrimSuffix(name, ".jpg"), ".png")
		if !levelshotNameRE.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		e, ok := s.get(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", e.ctype)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(e.data)
	}
}

// decodeTGA reads uncompressed and RLE truecolor TGAs (24 and 32 bit), which is what the game's
// levelshots are.
func decodeTGA(b []byte) (image.Image, error) {
	if len(b) < 18 {
		return nil, errors.New("short TGA")
	}
	idLen, cmapType, imgType := int(b[0]), b[1], b[2]
	w, h := int(b[12])|int(b[13])<<8, int(b[14])|int(b[15])<<8
	bpp, desc := int(b[16]), b[17]
	if cmapType != 0 || (imgType != 2 && imgType != 10) || (bpp != 24 && bpp != 32) || w <= 0 || h <= 0 || w > 8192 || h > 8192 {
		return nil, errors.New("unsupported TGA")
	}
	n := bpp / 8
	pos := 18 + idLen
	px := make([]byte, w*h*n)
	if imgType == 2 {
		if len(b) < pos+len(px) {
			return nil, errors.New("truncated TGA")
		}
		copy(px, b[pos:])
	} else {
		o := 0
		for o < len(px) {
			if pos >= len(b) {
				return nil, errors.New("truncated TGA")
			}
			c := int(b[pos])
			pos++
			cnt := (c & 0x7f) + 1
			if c&0x80 != 0 {
				if pos+n > len(b) {
					return nil, errors.New("truncated TGA")
				}
				for i := 0; i < cnt && o+n <= len(px); i++ {
					copy(px[o:o+n], b[pos:pos+n])
					o += n
				}
				pos += n
			} else {
				l := cnt * n
				if pos+l > len(b) || o+l > len(px) {
					return nil, errors.New("truncated TGA")
				}
				copy(px[o:o+l], b[pos:pos+l])
				o += l
				pos += l
			}
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		sy := h - 1 - y
		if desc&0x20 != 0 {
			sy = y
		}
		for x := 0; x < w; x++ {
			s := (sy*w + x) * n
			// levelshots are opaque pictures; the alpha channel of a 32-bit one is not meaningful
			img.SetNRGBA(x, y, color.NRGBA{R: px[s+2], G: px[s+1], B: px[s], A: 255})
		}
	}
	return img, nil
}
