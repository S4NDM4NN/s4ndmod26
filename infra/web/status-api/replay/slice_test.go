package replay

import (
	"bytes"
	"compress/zlib"
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

// ---- carried events ----

func testEvent(t int32, typ EventType, actor, extra int32, name string) []byte {
	b := make([]byte, eventSizeV8)
	binary.LittleEndian.PutUint32(b[offEvServerTime:], uint32(t))
	binary.LittleEndian.PutUint32(b[offEvActorClientNum:], uint32(actor))
	binary.LittleEndian.PutUint32(b[offEvTargetClientNum:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(b[offEvType:], uint32(typ))
	binary.LittleEndian.PutUint32(b[offEvExtra:], uint32(extra))
	binary.LittleEndian.PutUint32(b[offEvInflictorEntNum:], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(b[offEvLaunchEntNum:], 0xFFFFFFFF)
	copy(b[offEvName:], name)
	return b
}

// a v8 archive of real zlib chunks (5 s each, one empty frame per chunk) with the given events per chunk
func realRPL(t *testing.T, perChunk [][][]byte) string {
	t.Helper()
	b := make([]byte, archiveHeaderSizeV6)
	binary.LittleEndian.PutUint32(b[0:], ArchiveMagic)
	binary.LittleEndian.PutUint32(b[4:], 8)
	binary.LittleEndian.PutUint32(b[8:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16)  // maxclients
	binary.LittleEndian.PutUint32(b[28:], 396) // sampleSize (a known layout)
	binary.LittleEndian.PutUint32(b[32:], eventSizeV8)
	for i, evs := range perChunk {
		var raw bytes.Buffer
		binary.Write(&raw, binary.LittleEndian, int32(1))
		binary.Write(&raw, binary.LittleEndian, int32(len(evs)))
		binary.Write(&raw, binary.LittleEndian, int32(i*5000)) // the one frame: time, 0 samples
		binary.Write(&raw, binary.LittleEndian, int32(0))
		for _, e := range evs {
			raw.Write(e)
		}
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		zw.Write(raw.Bytes())
		zw.Close()
		ch := make([]byte, chunkHeaderSize)
		binary.LittleEndian.PutUint32(ch[0:], uint32(i*5000))
		binary.LittleEndian.PutUint32(ch[4:], uint32(i*5000+4950))
		binary.LittleEndian.PutUint32(ch[8:], 1)
		binary.LittleEndian.PutUint32(ch[12:], uint32(len(evs)))
		binary.LittleEndian.PutUint32(ch[16:], uint32(raw.Len()))
		binary.LittleEndian.PutUint32(ch[20:], uint32(z.Len()))
		b = append(b, ch...)
		b = append(b, z.Bytes()...)
	}
	p := filepath.Join(t.TempDir(), "real.rpl")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSliceRPLCarriesNamesAndBrokenEntities(t *testing.T) {
	p := realRPL(t, [][][]byte{
		{testEvent(100, EventPlayerJoin, 3, 0, "Ann"), testEvent(200, EventPlayerJoin, 4, 0, "Bob"), testEvent(300, EventMapBroken, -1, 77, "")},
		{testEvent(5100, EventPlayerRename, 3, 0, "Anna"), testEvent(5200, EventPlayerLeave, 4, 0, ""), testEvent(5300, EventMapBroken, -1, 78, "")},
		{testEvent(10100, EventKill, 3, 0, "")},
		{}, {}, // the clip: 15 s .. 25 s
		{testEvent(20100, EventPlayerRename, 3, 0, "AfterClipStart")},
		{testEvent(25100, EventMapBroken, -1, 99, "")}, // after the clip: never carried
	})
	out, err := SliceRPL(p, 15000, 24000)
	if err != nil {
		t.Fatal(err)
	}
	sp := filepath.Join(t.TempDir(), "slice.rpl")
	os.WriteFile(sp, out, 0o644)
	r, err := Parse(sp)
	if err != nil {
		t.Fatal(err)
	}
	var names = map[int32]string{}
	var broken []int32
	for _, ev := range r.Events {
		switch ev.Type {
		case EventPlayerJoin, EventPlayerRename:
			if ev.ServerTime < 15000 { // what was carried, not what the clip itself holds
				names[ev.ActorClientNum] = ev.Name
			}
		case EventMapBroken:
			broken = append(broken, ev.Extra)
		}
	}
	if names[3] != "Anna" || names[4] != "Bob" || len(names) != 2 {
		t.Errorf("carried names %v, want slot 3 Anna (the latest) and slot 4 Bob", names)
	}
	if len(broken) != 2 || broken[0] != 77 || broken[1] != 78 {
		t.Errorf("carried broken entities %v, want [77 78]", broken)
	}
	if int(r.Header.EventCount) != len(r.Events) {
		t.Errorf("header says %d events, slice has %d", r.Header.EventCount, len(r.Events))
	}
	// the carried chunk comes first and is dated inside the window, or the game would discard it
	if ch := parseChunkHeader(out[archiveHeaderSizeV6:]); ch.FrameCount != 0 || ch.StartTime < 10000 {
		t.Errorf("first chunk %+v", ch)
	}
	for i := 1; i < len(r.Events); i++ {
		if r.Events[i].ServerTime < r.Events[i-1].ServerTime {
			t.Errorf("events out of order at %d: %d after %d", i, r.Events[i].ServerTime, r.Events[i-1].ServerTime)
		}
	}
}

func TestSliceRPLNothingToCarry(t *testing.T) {
	p := realRPL(t, [][][]byte{{}, {}, {testEvent(10100, EventKill, 3, 0, "")}})
	out, err := SliceRPL(p, 10000, 15000)
	if err != nil {
		t.Fatal(err)
	}
	if ch := parseChunkHeader(out[archiveHeaderSizeV6:]); ch.FrameCount != 1 {
		t.Errorf("a carried chunk was added with nothing to carry: %+v", ch)
	}
}
