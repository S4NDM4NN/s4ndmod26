package replay

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const knownEventSize = 40 // sizeof(replayEvent_t): offEvOrigin(28) + [3]float32(12)

// FindLiveReplay returns the newest .rpl file in dir that has no matching .txt
// (the .txt is only written at intermission, so its absence means the match is live).
func FindLiveReplay(dir string) (rplPath, baseName string, found bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false
	}

	var newestPath, newestBase string
	var newestTime time.Time

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".rpl") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".rpl")
		if _, err := os.Stat(filepath.Join(dir, base+".txt")); err == nil {
			continue // match already completed
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newestPath = filepath.Join(dir, e.Name())
			newestBase = base
		}
	}

	return newestPath, newestBase, newestPath != ""
}

// ParseLive reads a still-being-written stream file. The game writes a valid
// placeholder archive header (with magic, sampleSize, eventSize, maxclients)
// at position 0 when recording starts, then appends chunks after it.
// frameCount/eventCount/mapname in the header are filled in only at
// intermission, so we parse the header for layout info but ignore those counts.
func ParseLive(path string) (*Replay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if len(data) < archiveHeaderSizeV4 {
		return &Replay{}, nil
	}

	header, err := parseArchiveHeader(data)
	if err != nil || header.Magic != ArchiveMagic ||
		header.Version < ArchiveVersion || header.Version > ArchiveVersionCurrent {
		return &Replay{}, nil
	}

	knownLayout := header.SampleSize == knownSampleSize
	r := &Replay{Header: header}
	pos := header.HeaderSize()

	for pos+chunkHeaderSize <= len(data) {
		ch := parseChunkHeader(data[pos : pos+chunkHeaderSize])
		pos += chunkHeaderSize

		// Reject obviously corrupt headers before trusting their sizes.
		if ch.CompressedBytes <= 0 || ch.CompressedBytes > 50_000_000 ||
			ch.UncompressedBytes <= 0 || ch.UncompressedBytes > 200_000_000 {
			break
		}

		end := pos + int(ch.CompressedBytes)
		if end > len(data) {
			break // incomplete chunk — wait for next flush
		}

		payload, err := decompressChunk(data[pos:end], ch.UncompressedBytes)
		if err != nil {
			pos = end
			continue
		}

		frames, events, err := parseChunkPayload(payload, header.SampleSize, header.EventSize, knownLayout)
		if err != nil {
			pos = end
			continue
		}

		r.Frames = append(r.Frames, frames...)
		r.Events = append(r.Events, events...)
		pos = end
	}

	return r, nil
}

// AnalyzeLive runs the standard Analyze pipeline without a .txt metadata file.
func AnalyzeLive(r *Replay) *Analysis {
	if r.Header.MaxClients == 0 {
		r.Header.MaxClients = 64
	}
	if r.Header.RecordMsec == 0 {
		r.Header.RecordMsec = 50
	}
	return Analyze(r, "")
}
