package replay

import (
	"encoding/binary"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// synthetic archive: v8 header + chunks of 5 s with fake payloads (SliceRPL never decompresses).
func fakeRPL(t *testing.T, chunks int) string {
	t.Helper()
	b := make([]byte, archiveHeaderSizeV6)
	binary.LittleEndian.PutUint32(b[0:], ArchiveMagic)
	binary.LittleEndian.PutUint32(b[4:], 8)
	for i := 0; i < chunks; i++ {
		ch := make([]byte, chunkHeaderSize)
		binary.LittleEndian.PutUint32(ch[0:], uint32(i*5000))
		binary.LittleEndian.PutUint32(ch[4:], uint32(i*5000+4950))
		binary.LittleEndian.PutUint32(ch[8:], 100) // frames
		binary.LittleEndian.PutUint32(ch[12:], 3)  // events
		binary.LittleEndian.PutUint32(ch[16:], 1000)
		binary.LittleEndian.PutUint32(ch[20:], 10)
		b = append(b, ch...)
		b = append(b, make([]byte, 10)...)
	}
	p := filepath.Join(t.TempDir(), "g.rpl")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSliceRPLKeepsOverlappingChunks(t *testing.T) {
	p := fakeRPL(t, 10) // 0..50 s
	out, err := SliceRPL(p, 12000, 21000)
	if err != nil {
		t.Fatal(err)
	}
	// chunks starting 10, 15, 20 s overlap
	want := archiveHeaderSizeV6 + 3*(chunkHeaderSize+10)
	if len(out) != want {
		t.Fatalf("len %d, want %d", len(out), want)
	}
	if got := readInt32LE(out, headerFrameCountOff); got != 300 {
		t.Errorf("frames %d, want 300", got)
	}
	if got := readInt32LE(out, headerEventCountOff); got != 9 {
		t.Errorf("events %d, want 9", got)
	}
	if got := parseChunkHeader(out[archiveHeaderSizeV6:]).StartTime; got != 10000 {
		t.Errorf("first chunk starts %d, want 10000", got)
	}
}

func TestSliceRPLTruncatedTail(t *testing.T) {
	p := fakeRPL(t, 3)
	b, _ := os.ReadFile(p)
	os.WriteFile(p, b[:len(b)-4], 0o644) // last payload cut
	out, err := SliceRPL(p, -1<<30, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if want := archiveHeaderSizeV6 + 2*(chunkHeaderSize+10); len(out) != want {
		t.Fatalf("len %d, want %d", len(out), want)
	}
}

// the real 160 s sample play of the game: the slice parses and holds exactly the frames the game would keep
func TestSliceRPLSamplePOTG(t *testing.T) {
	dir := os.Getenv("POTG_SAMPLE_DIR")
	if dir == "" {
		dir = "/home/travis/potg-for-prod"
	}
	rpl := filepath.Join(dir, "s4ndm4nn_demo_potg.rpl")
	if _, err := os.Stat(rpl); err != nil {
		t.Skip("no sample")
	}
	from, to, err := ClipWindow(filepath.Join(dir, "s4ndm4nn_demo_potg.txt"), ClipPOTG, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, err := SliceRPL(rpl, from, to)
	if err != nil {
		t.Fatal(err)
	}
	sp := filepath.Join(t.TempDir(), "s.rpl")
	os.WriteFile(sp, out, 0o644)
	full, err := Parse(rpl)
	if err != nil {
		t.Fatal(err)
	}
	sl, err := Parse(sp)
	if err != nil {
		t.Fatal(err)
	}
	if len(sl.Frames) == 0 || len(sl.Frames) >= len(full.Frames)/2 {
		t.Fatalf("slice has %d of %d frames", len(sl.Frames), len(full.Frames))
	}
	if int(sl.Header.FrameCount) != len(sl.Frames) {
		t.Errorf("header says %d frames, slice has %d", sl.Header.FrameCount, len(sl.Frames))
	}
	for _, f := range sl.Frames {
		if f.ServerTime < from-5000 || f.ServerTime > to+5000 {
			t.Fatalf("frame at %d outside window [%d,%d]", f.ServerTime, from, to)
		}
	}
	t.Logf("full %d bytes -> slice %d bytes (%d of %d frames)", fileSize(rpl), len(out), len(sl.Frames), len(full.Frames))
}

func fileSize(p string) int64 { s, _ := os.Stat(p); return s.Size() }

func TestServeClipNames(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"g.rpl", "g.txt", "g.json", "g.potg.json", "../x.potg.rpl", "g.hl.rpl", "g.hlx.rpl"} {
		rec := httptest.NewRecorder()
		if ServeClip(rec, httptest.NewRequest("GET", "/", nil), dir, n) {
			t.Errorf("%q was taken for a clip", n)
		}
	}
	rec := httptest.NewRecorder() // a clip name with no recording: answered (404) here, never falls through to a file
	if !ServeClip(rec, httptest.NewRequest("GET", "/", nil), dir, "g.potg.rpl") || rec.Code != 404 {
		t.Errorf("missing recording: handled %d", rec.Code)
	}
}
