package bsp

import (
	"encoding/binary"
	"math"
	"testing"
)

func put32(b []byte, o int, v int)    { binary.LittleEndian.PutUint32(b[o:], uint32(int32(v))) }
func putf(b []byte, o int, v float32) { binary.LittleEndian.PutUint32(b[o:], math.Float32bits(v)) }

// buildBSP makes a tiny IBSP 47 file: a shader list (name, surface flags), the vertices, the indexes and one planar surface per
// entry of surfaces, all in the world model.
func buildBSP(shaders []int, verts [][3]float32, indexes []int, surfaces [][4]int) []byte {
	lumps := make([][]byte, 17)
	lumps[lumpShaders] = make([]byte, len(shaders)*shaderSize)
	for i, f := range shaders {
		put32(lumps[lumpShaders], i*shaderSize+64, f)
	}
	lumps[lumpModels] = make([]byte, modelSize)
	put32(lumps[lumpModels], 24, 0)
	put32(lumps[lumpModels], 28, len(surfaces))
	lumps[lumpVerts] = make([]byte, len(verts)*vertSize)
	for i, v := range verts {
		for k := 0; k < 3; k++ {
			putf(lumps[lumpVerts], i*vertSize+k*4, v[k])
		}
	}
	lumps[lumpIndexes] = make([]byte, len(indexes)*4)
	for i, v := range indexes {
		put32(lumps[lumpIndexes], i*4, v)
	}
	lumps[lumpSurfaces] = make([]byte, len(surfaces)*surfaceSize)
	for i, s := range surfaces { // shader, firstVert, numVerts, numIndexes (first index is 0)
		o := i * surfaceSize
		put32(lumps[lumpSurfaces], o, s[0])
		put32(lumps[lumpSurfaces], o+8, surfPlanar)
		put32(lumps[lumpSurfaces], o+12, s[1])
		put32(lumps[lumpSurfaces], o+16, s[2])
		put32(lumps[lumpSurfaces], o+24, s[3])
	}
	out := make([]byte, 8+17*8)
	copy(out, "IBSP")
	put32(out, 4, 47)
	for i, l := range lumps {
		put32(out, 8+i*8, len(out))
		put32(out, 12+i*8, len(l))
		out = append(out, l...)
	}
	return out
}

func TestParse(t *testing.T) {
	verts := [][3]float32{{0, 0, 0}, {100, 0, 0}, {0, 100, 0}, {100, 100, 0}}
	// surface 0 is a quad (two triangles); surface 1 uses the sky shader and is skipped; surface 2 asks for vertex 3 of a surface that
	// has only 1 vertex, so it is dropped as corrupt
	m, err := Parse(buildBSP([]int{0, surfSky}, verts,
		[]int{0, 1, 2, 2, 1, 3},
		[][4]int{{0, 0, 4, 6}, {1, 0, 4, 6}, {0, 1, 1, 3}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tris) != 6 || len(m.Verts) != 4 {
		t.Fatalf("want 2 triangles on 4 welded vertices, got tris %v verts %v", m.Tris, m.Verts)
	}
	if m.Verts[m.Tris[1]] != [3]int32{100, 0, 0} {
		t.Errorf("second corner: %v", m.Verts[m.Tris[1]])
	}
}

func TestParseSkipsSkyAndRejectsJunk(t *testing.T) {
	verts := [][3]float32{{0, 0, 0}, {100, 0, 0}, {0, 100, 0}}
	m, err := Parse(buildBSP([]int{surfSky | surfNoDraw}, verts, []int{0, 1, 2}, [][4]int{{0, 0, 3, 3}}))
	if err != nil || len(m.Tris) != 0 {
		t.Errorf("sky surface should be dropped: %v %v", m, err)
	}
	if _, err := Parse([]byte("not a bsp at all, but long enough to pass the size check .............................................................................................................")); err == nil {
		t.Error("junk accepted")
	}
	if _, err := Parse(nil); err == nil {
		t.Error("nil accepted")
	}
}
