package main

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"s4ndmod26/status-api/bsp"
)

func TestEncodeMesh(t *testing.T) {
	m := &bsp.Mesh{Verts: [][3]int32{{1, 2, 3}, {-4, 5, 6}, {7, 8, 9}}, Tris: []uint32{0, 1, 2}}
	b := encodeMesh(m)
	le := binary.LittleEndian
	if le.Uint32(b[0:]) != 0 || le.Uint32(b[4:]) != 3 || le.Uint32(b[8:]) != 1 {
		t.Fatalf("header: %v", b[:12])
	}
	if int32(le.Uint32(b[24:])) != -4 || len(b) != 12+3*12+3*2 {
		t.Errorf("body: len %d", len(b))
	}
	if le.Uint16(b[12+36+2:]) != 1 {
		t.Errorf("second index: %v", b[48:])
	}
}

func TestMapMeshHandlerMisses(t *testing.T) {
	dir := t.TempDir()
	writeZip(t, filepath.Join(dir, "a.pk3"), map[string][]byte{"maps/mp_junk.bsp": []byte("not a bsp")})
	h := mapMeshHandler(&mapMeshStore{shots: &levelshotStore{dirs: []string{dir}}})
	get := func(name string) int {
		r := httptest.NewRequest("GET", "/api/maps/"+name+"/mesh", nil)
		r.SetPathValue("map", name)
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	if c := get("mp_nowhere"); c != http.StatusNotFound {
		t.Errorf("missing map: %d", c)
	}
	if c := get("mp_junk"); c != http.StatusNotFound {
		t.Errorf("unreadable bsp: %d", c)
	}
	if c := get("../etc"); c != http.StatusBadRequest {
		t.Errorf("bad name: %d", c)
	}
}
