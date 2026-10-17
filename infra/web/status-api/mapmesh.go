package main

// GET /api/maps/{map}/mesh returns the map's visible world geometry as a small binary triangle mesh, read out of the map's .bsp in
// whichever pk3 the server has it in (the same search as the levelshots).  The movement view draws it faintly behind the trails.
// 404 means no pk3 has the map (the full game's maps are not ours to serve).
//
// Format, little endian: u32 flags (bit 0: indexes are u32, else u16), u32 vertex count, u32 triangle count, then the vertices as
// i32 x, y, z, then the indexes (three per triangle).

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"s4ndmod26/status-api/bsp"
)

const mapMeshMaxBSP = 64 << 20

type meshEntry struct {
	raw, gz []byte
	found   bool
	expires time.Time
}

type mapMeshStore struct {
	shots *levelshotStore // for its pk3 search order
	mu    sync.Mutex
	cache map[string]meshEntry
}

func encodeMesh(m *bsp.Mesh) []byte {
	var flags uint32
	if len(m.Verts) > 65535 {
		flags = 1
	}
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, []uint32{flags, uint32(len(m.Verts)), uint32(len(m.Tris) / 3)})
	for _, v := range m.Verts {
		binary.Write(&b, binary.LittleEndian, v)
	}
	if flags == 1 {
		binary.Write(&b, binary.LittleEndian, m.Tris)
	} else {
		idx := make([]uint16, len(m.Tris))
		for i, t := range m.Tris {
			idx[i] = uint16(t)
		}
		binary.Write(&b, binary.LittleEndian, idx)
	}
	return b.Bytes()
}

func (s *mapMeshStore) build(name string) (meshEntry, bool) {
	want := "maps/" + strings.ToLower(name) + ".bsp"
	for _, path := range s.shots.pk3Paths() {
		zr, err := zip.OpenReader(path)
		if err != nil {
			continue
		}
		for _, f := range zr.File {
			if strings.ToLower(f.Name) != want || f.UncompressedSize64 > mapMeshMaxBSP {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(rc, mapMeshMaxBSP))
			rc.Close()
			zr.Close()
			if err != nil {
				return meshEntry{}, false
			}
			mesh, err := bsp.Parse(data)
			if err != nil {
				log.Printf("mapmesh %s: %v", name, err)
				return meshEntry{}, false
			}
			raw := encodeMesh(mesh)
			var gz bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
			zw.Write(raw)
			zw.Close()
			return meshEntry{raw: raw, gz: gz.Bytes(), found: true}, true
		}
		zr.Close()
	}
	return meshEntry{}, false
}

func (s *mapMeshStore) get(name string) meshEntry {
	s.mu.Lock()
	if e, ok := s.cache[name]; ok && time.Now().Before(e.expires) {
		s.mu.Unlock()
		return e
	}
	s.mu.Unlock()
	e, _ := s.build(name)
	if e.found {
		e.expires = time.Now().Add(time.Hour)
	} else {
		e.expires = time.Now().Add(time.Minute)
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]meshEntry{}
	}
	if len(s.cache) > 32 {
		s.cache = map[string]meshEntry{}
	}
	s.cache[name] = e
	s.mu.Unlock()
	return e
}

func mapMeshHandler(s *mapMeshStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("map")
		if !levelshotNameRE.MatchString(name) {
			http.Error(w, "bad map name", http.StatusBadRequest)
			return
		}
		e := s.get(name)
		if !e.found {
			http.Error(w, "no geometry for that map", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Add("Vary", "Accept-Encoding")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(e.gz)
			return
		}
		w.Write(e.raw)
	}
}
