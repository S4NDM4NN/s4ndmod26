package replay

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
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
// header's frame and event counts are rewritten to the slice.  It reads the file chunk header by chunk
// header, so only the wanted payloads are read.
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
		if !(c.EndTime < fromMs || c.StartTime > toMs) {
			payload := make([]byte, c.CompressedBytes)
			if _, err := f.ReadAt(payload, pos); err != nil {
				return nil, err
			}
			out = append(out, ch[:]...)
			out = append(out, payload...)
			frames += c.FrameCount
			events += c.EventCount
		}
		pos += int64(c.CompressedBytes)
	}
	binary.LittleEndian.PutUint32(out[headerFrameCountOff:], uint32(frames))
	binary.LittleEndian.PutUint32(out[headerEventCountOff:], uint32(events))
	return out, nil
}
