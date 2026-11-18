package replay

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Clip slicing: the viewer plays one clip (the play of the game or one player's highlight) and the game only
// loads the chunks around it, so the rest of a 2-17 MB match recording never needs to leave the server.  A chunk
// holds full state (no deltas, no keyframes), so a run of chunks plus the header is a complete .rpl.
//
// The margins below MUST match the game's own load window (REPLAY_LOAD_BEFORE_MSEC, REPLAY_LOAD_MARGIN_MSEC,
// REPLAY_HIGHLIGHT_LOAD_BEFORE, REPLAY_HIGHLIGHT_LOAD_AFTER in mod/src/game/g_replay.c, G_ReplayLoadFromFile):
// the slice then holds exactly the chunks the game would have kept.
const (
	potgLoadBeforeMs      = 20000
	potgLoadAfterMs       = 4000
	highlightLoadBeforeMs = 40000
	highlightLoadAfterMs  = 15000

	headerFrameCountOff = 36
	headerEventCountOff = 40
)

// ClipKind says which clip of a game to cut.
type ClipKind int

const (
	ClipPOTG      ClipKind = iota // the play of the game
	ClipHighlight                 // one player's best window
)

// ClipWindow reads the sidecar (.txt) and returns the time range of chunks the game loads for the clip.  For a
// highlight, slot is the player's client number.
func ClipWindow(txtPath string, kind ClipKind, slot int) (fromMs, toMs int32, err error) {
	meta := parseMeta(txtPath)
	if len(meta) == 0 {
		return 0, 0, fmt.Errorf("no sidecar")
	}
	if kind == ClipHighlight {
		for _, h := range parseHighlights(meta) {
			if h.Player == slot {
				return h.WindowStartMs - highlightLoadBeforeMs, h.WindowEndMs + highlightLoadAfterMs, nil
			}
		}
		return 0, 0, fmt.Errorf("no highlight for slot %d", slot)
	}
	target, ok := metaInt(meta, "selectionTarget")
	cs, ok1 := metaInt(meta, "selectionClipStart")
	ce, ok2 := metaInt(meta, "selectionClipEnd")
	if !ok || !ok1 || !ok2 || target < 0 || ce <= cs {
		return 0, 0, fmt.Errorf("no play of the game")
	}
	return int32(cs) - potgLoadBeforeMs, int32(ce) + potgLoadAfterMs, nil
}

func metaInt(meta map[string]string, key string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(meta[key]))
	return n, err == nil
}

// SliceRPL copies the archive header and every chunk overlapping [fromMs, toMs] from the file at path (the
// game's own overlap test: not (end < from or start > to)).  Compressed payloads are copied as they are.  The
// header's frame and event counts are rewritten to the slice.
//
// A clip that starts late in a match would lose what was set earlier, which the viewer needs: who is in each
// slot, and which breakable map entities (walls, crates) were already gone.  The game only keeps what it loads,
// so the slice starts with one extra chunk of events and no frames (carriedEvents): the latest join or rename of
// each slot, and every map entity destroyed, from the chunks the slice leaves out before its first one.
func SliceRPL(path string, fromMs, toMs int32) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()

	head := make([]byte, archiveHeaderSizeV4)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	h, err := parseArchiveHeader(head)
	if err != nil {
		return nil, err
	}
	if h.Magic != ArchiveMagic {
		return nil, fmt.Errorf("bad magic: 0x%08X", h.Magic)
	}
	if h.Version < ArchiveVersion || h.Version > ArchiveVersionCurrent {
		return nil, fmt.Errorf("unsupported version: %d", h.Version)
	}
	hs := int64(h.HeaderSize())
	if hs > size {
		return nil, fmt.Errorf("header out of bounds")
	}
	out := make([]byte, hs)
	if _, err := f.ReadAt(out, 0); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	var frames, events int32
	var body []byte
	carry := newCarry(h)
	firstKept := int32(0)
	haveKept := false
	pos := hs
	var ch [chunkHeaderSize]byte
	for pos+chunkHeaderSize <= size {
		if _, err := f.ReadAt(ch[:], pos); err != nil {
			return nil, err
		}
		c := parseChunkHeader(ch[:])
		pos += chunkHeaderSize
		if c.CompressedBytes <= 0 || int64(c.CompressedBytes) > size-pos {
			break // a truncated tail (a recording that was cut off): keep what is whole
		}
		wanted := !(c.EndTime < fromMs || c.StartTime > toMs)
		switch {
		case wanted:
			payload := make([]byte, c.CompressedBytes)
			if _, err := f.ReadAt(payload, pos); err != nil {
				return nil, err
			}
			if !haveKept {
				haveKept, firstKept = true, c.StartTime
			}
			body = append(body, ch[:]...)
			body = append(body, payload...)
			frames += c.FrameCount
			events += c.EventCount
		case !haveKept && c.EndTime < fromMs:
			// before the clip: its frames are not needed, its names and destroyed entities are
			payload := make([]byte, c.CompressedBytes)
			if _, err := f.ReadAt(payload, pos); err != nil {
				return nil, err
			}
			if raw, err := decompressChunk(payload, c.UncompressedBytes); err == nil {
				carry.scan(raw)
			}
		}
		pos += int64(c.CompressedBytes)
	}
	if front, n := carry.chunk(firstKept); haveKept && n > 0 {
		out = append(out, front...)
		events += int32(n)
	}
	out = append(out, body...)
	binary.LittleEndian.PutUint32(out[headerFrameCountOff:], uint32(frames))
	binary.LittleEndian.PutUint32(out[headerEventCountOff:], uint32(events))
	return out, nil
}

// carriedEvents collects, from chunks a slice leaves out, the events the viewer still needs.
type carriedEvents struct {
	sampleSize, eventSize int
	names                 map[int32][]byte // slot -> its latest join or rename event
	broken                [][]byte         // every destroyed map entity
}

func newCarry(h ArchiveHeader) *carriedEvents {
	return &carriedEvents{sampleSize: int(h.SampleSize), eventSize: int(h.EventSize), names: map[int32][]byte{}}
}

// scan reads one decompressed chunk payload (frames, then events) and keeps the events worth carrying.
func (c *carriedEvents) scan(payload []byte) {
	if len(payload) < 8 || c.eventSize < offEvExtra+4 {
		return
	}
	frameCount, eventCount := int(readInt32LE(payload, 0)), int(readInt32LE(payload, 4))
	pos := 8
	for i := 0; i < frameCount; i++ {
		if pos+8 > len(payload) {
			return
		}
		n := int(readInt32LE(payload, pos+4))
		if n < 0 {
			return
		}
		pos += 8 + n*c.sampleSize
	}
	for i := 0; i < eventCount; i++ {
		end := pos + c.eventSize
		if pos < 0 || end > len(payload) {
			return
		}
		ev := payload[pos:end]
		pos = end
		switch EventType(readInt32LE(ev, offEvType)) {
		case EventPlayerJoin, EventPlayerRename:
			if a := readInt32LE(ev, offEvActorClientNum); a >= 0 && a < maxClientsInHeader {
				c.names[a] = append([]byte(nil), ev...) // later events replace earlier ones
			}
		case EventMapBroken:
			c.broken = append(c.broken, append([]byte(nil), ev...))
		}
	}
}

// chunk builds the events-only chunk (frameCount 0), dated at the slice's first chunk so the game keeps it, and
// returns it with its event count.  Events stay in time order, which the game relies on.
func (c *carriedEvents) chunk(at int32) ([]byte, int) {
	evs := make([][]byte, 0, len(c.names)+len(c.broken))
	for _, ev := range c.names {
		evs = append(evs, ev)
	}
	evs = append(evs, c.broken...)
	if len(evs) == 0 {
		return nil, 0
	}
	sort.SliceStable(evs, func(i, j int) bool {
		return readInt32LE(evs[i], offEvServerTime) < readInt32LE(evs[j], offEvServerTime)
	})

	var raw bytes.Buffer
	binary.Write(&raw, binary.LittleEndian, int32(0))
	binary.Write(&raw, binary.LittleEndian, int32(len(evs)))
	for _, ev := range evs {
		raw.Write(ev)
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(raw.Bytes())
	zw.Close()

	hdr := make([]byte, chunkHeaderSize)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(at))
	binary.LittleEndian.PutUint32(hdr[4:], uint32(at))
	binary.LittleEndian.PutUint32(hdr[8:], 0)
	binary.LittleEndian.PutUint32(hdr[12:], uint32(len(evs)))
	binary.LittleEndian.PutUint32(hdr[16:], uint32(raw.Len()))
	binary.LittleEndian.PutUint32(hdr[20:], uint32(z.Len()))
	return append(hdr, z.Bytes()...), len(evs)
}
