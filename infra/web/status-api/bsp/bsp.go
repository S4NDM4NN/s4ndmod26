// Package bsp reads the visible world geometry out of an RTCW / Quake 3 style .bsp (IBSP, version 46 or 47) and thins it to a
// lightweight triangle mesh, for drawing a map's outline behind the movement view.  It keeps no textures, lightmaps or entities.
package bsp

import (
	"encoding/binary"
	"errors"
	"math"
)

const (
	lumpShaders  = 1
	lumpModels   = 7
	lumpVerts    = 10
	lumpIndexes  = 11
	lumpSurfaces = 13

	shaderSize  = 72
	modelSize   = 40
	vertSize    = 44
	surfaceSize = 104

	surfPlanar = 1
	surfPatch  = 2
	surfSoup   = 3

	// surface flags that mean "not drawn as geometry"
	surfSky    = 0x4
	surfNoDraw = 0x80
	surfHint   = 0x100
	surfSkip   = 0x200

	patchTess = 5 // quads per patch edge when tessellating curves
)

// Mesh is the world geometry: positions as whole game units (x, y, z per vertex) and triangles as vertex index triples.
type Mesh struct {
	Verts [][3]int32
	Tris  []uint32 // three vertex indexes per triangle
}

type lump struct{ off, len int }

func le32(b []byte, o int) int    { return int(int32(binary.LittleEndian.Uint32(b[o:]))) }
func lef(b []byte, o int) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b[o:])) }

// Parse reads the world model's visible triangles.  Triangles are welded by position (vertices that are the same whole unit share
// an index) and ones that collapse are dropped.
func Parse(data []byte) (*Mesh, error) {
	if len(data) < 8+17*8 || string(data[:4]) != "IBSP" {
		return nil, errors.New("not an IBSP file")
	}
	if v := le32(data, 4); v != 46 && v != 47 {
		return nil, errors.New("unsupported BSP version")
	}
	var lumps [17]lump
	for i := range lumps {
		lumps[i] = lump{le32(data, 8+i*8), le32(data, 12+i*8)}
		if lumps[i].off < 0 || lumps[i].len < 0 || lumps[i].off+lumps[i].len > len(data) {
			return nil, errors.New("lump out of bounds")
		}
	}
	sec := func(i int) []byte { return data[lumps[i].off : lumps[i].off+lumps[i].len] }
	shaders, models, verts, idx, surfs := sec(lumpShaders), sec(lumpModels), sec(lumpVerts), sec(lumpIndexes), sec(lumpSurfaces)
	nVerts, nIdx, nSurf := len(verts)/vertSize, len(idx)/4, len(surfs)/surfaceSize
	if len(models) < modelSize {
		return nil, errors.New("no world model")
	}
	// model 0 is the world: bounds (6 floats), then firstSurface, numSurfaces, firstBrush, numBrushes
	first, count := le32(models, 24), le32(models, 28)
	if first < 0 || count < 0 || first+count > nSurf {
		return nil, errors.New("bad world model")
	}

	m := &Mesh{}
	weld := map[[3]int32]uint32{}
	vertAt := func(i int) [3]int32 {
		o := i * vertSize
		return [3]int32{int32(math.Round(float64(lef(verts, o)))), int32(math.Round(float64(lef(verts, o+4)))), int32(math.Round(float64(lef(verts, o+8))))}
	}
	index := func(p [3]int32) uint32 {
		if i, ok := weld[p]; ok {
			return i
		}
		i := uint32(len(m.Verts))
		weld[p] = i
		m.Verts = append(m.Verts, p)
		return i
	}
	tri := func(a, b, c [3]int32) {
		if a == b || b == c || a == c {
			return
		}
		m.Tris = append(m.Tris, index(a), index(b), index(c))
	}

	for s := first; s < first+count; s++ {
		o := s * surfaceSize
		shader, typ := le32(surfs, o), le32(surfs, o+8)
		fv, nv, fi, ni := le32(surfs, o+12), le32(surfs, o+16), le32(surfs, o+20), le32(surfs, o+24)
		if shader < 0 || (shader+1)*shaderSize > len(shaders) {
			continue
		}
		if flags := le32(shaders, shader*shaderSize+64); flags&(surfSky|surfNoDraw|surfHint|surfSkip) != 0 {
			continue
		}
		if fv < 0 || nv < 0 || fv+nv > nVerts {
			continue
		}
		switch typ {
		case surfPlanar, surfSoup:
			if fi < 0 || ni < 0 || fi+ni > nIdx {
				continue
			}
			for i := 0; i+2 < ni; i += 3 {
				var p [3][3]int32
				ok := true
				for k := 0; k < 3; k++ {
					v := le32(idx, (fi+i+k)*4)
					if v < 0 || v >= nv {
						ok = false
						break
					}
					p[k] = vertAt(fv + v)
				}
				if ok {
					tri(p[0], p[1], p[2])
				}
			}
		case surfPatch:
			pw, ph := le32(surfs, o+96), le32(surfs, o+100)
			if pw < 3 || ph < 3 || pw*ph > nv {
				continue
			}
			patch(verts, fv, pw, ph, tri)
		}
	}
	return m, nil
}

// patch tessellates a grid of biquadratic Bezier patches (each 3x3 block of control points, overlapping by one) into triangles.
func patch(verts []byte, first, pw, ph int, tri func(a, b, c [3]int32)) {
	ctl := func(x, y int) [3]float64 {
		o := (first + y*pw + x) * vertSize
		return [3]float64{float64(lef(verts, o)), float64(lef(verts, o+4)), float64(lef(verts, o+8))}
	}
	bez := func(a, b, c [3]float64, t float64) [3]float64 {
		u := 1 - t
		var r [3]float64
		for i := 0; i < 3; i++ {
			r[i] = u*u*a[i] + 2*u*t*b[i] + t*t*c[i]
		}
		return r
	}
	for py := 0; py+2 < ph; py += 2 {
		for px := 0; px+2 < pw; px += 2 {
			var grid [patchTess + 1][patchTess + 1][3]int32
			for j := 0; j <= patchTess; j++ {
				v := float64(j) / patchTess
				var col [3][3]float64
				for i := 0; i < 3; i++ {
					col[i] = bez(ctl(px+i, py), ctl(px+i, py+1), ctl(px+i, py+2), v)
				}
				for i := 0; i <= patchTess; i++ {
					p := bez(col[0], col[1], col[2], float64(i)/patchTess)
					grid[j][i] = [3]int32{int32(math.Round(p[0])), int32(math.Round(p[1])), int32(math.Round(p[2]))}
				}
			}
			for j := 0; j < patchTess; j++ {
				for i := 0; i < patchTess; i++ {
					tri(grid[j][i], grid[j+1][i], grid[j][i+1])
					tri(grid[j][i+1], grid[j+1][i], grid[j+1][i+1])
				}
			}
		}
	}
}
