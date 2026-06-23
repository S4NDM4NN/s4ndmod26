package replay

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

const (
	ArchiveMagic          = 0x52504C59
	ArchiveVersion        = 4 // minimum supported version
	ArchiveVersionCurrent = 7 // current write version

	archiveHeaderSizeV4 = 108
	archiveHeaderSizeV5 = 2412 // 108 + MAX_CLIENTS(64) * MAX_NETNAME(36)
	archiveHeaderSizeV6 = 2668 // v5 + spawnPointNames[8][32]
	maxClientsInHeader  = 64
	maxNetnameInHeader  = 36
	maxSpawnPoints      = 8
	spawnPointNameSize  = 32
	chunkHeaderSize     = 24
	mapNameSize         = 64

	// Byte offsets within a replaySample_t that are stable across known layouts.
	offClientNum   = 0
	offHealth      = 4
	offTeam        = 8
	offPmType      = 12
	offPmFlags     = 16
	offWeaponstate = 24

	// Byte offsets within a replayEvent_t.
	offEvServerTime      = 0
	offEvActorClientNum  = 4
	offEvTargetClientNum = 8
	offEvScore           = 12
	offEvType            = 16
	offEvMeansOfDeath    = 20
	offEvExtra           = 24
	offEvOrigin          = 28
)

// EventType mirrors replayEventType_t from g_replay.c.
type EventType int32

const (
	EventKill EventType = iota
	EventHeadshot
	EventExplosiveKill
	EventKnifeKill
	EventMultikill
	EventTeamkill
	EventSuicide
	EventRevive
	EventObjectiveSteal
	EventObjectiveReturn
	EventObjectiveCapture
	EventObjectiveDenial
	EventTapout
	EventMedpackPickup
	EventDamage
	EventAmmoGive
	EventObjectivePlant
	EventObjectiveDefuse
	EventSpawnCapture
	EventMatchEnd
)

var eventTypeNames = map[EventType]string{
	EventKill:             "KILL",
	EventHeadshot:         "HEADSHOT",
	EventExplosiveKill:    "EXPLOSIVE_KILL",
	EventKnifeKill:        "KNIFE_KILL",
	EventMultikill:        "MULTIKILL",
	EventTeamkill:         "TEAMKILL",
	EventSuicide:          "SUICIDE",
	EventRevive:           "REVIVE",
	EventObjectiveSteal:   "OBJECTIVE_STEAL",
	EventObjectiveReturn:  "OBJECTIVE_RETURN",
	EventObjectiveCapture: "OBJECTIVE_CAPTURE",
	EventObjectiveDenial:  "OBJECTIVE_DENIAL",
	EventTapout:           "TAPOUT",
	EventMedpackPickup:    "MEDPACK_PICKUP",
	EventDamage:           "DAMAGE",
	EventAmmoGive:         "AMMO_GIVE",
	EventObjectivePlant:   "OBJECTIVE_PLANT",
	EventObjectiveDefuse:  "OBJECTIVE_DEFUSE",
	EventSpawnCapture: "SPAWN_CAPTURE",
	EventMatchEnd:     "MATCH_END",
}

func (e EventType) String() string {
	if s, ok := eventTypeNames[e]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN_%d", int(e))
}

type ArchiveHeader struct {
	Magic       int32
	Version     int32
	Codec       int32
	RecordMsec  int32
	ChunkMsec   int32
	Gametype    int32
	MaxClients  int32
	SampleSize  int32
	EventSize   int32
	FrameCount  int32
	EventCount  int32
	MapName     string
	PlayerNames     [maxClientsInHeader]string // populated for version >= 5
	SpawnPointNames [maxSpawnPoints]string     // populated for version >= 6
}

// HeaderSize returns the byte size of the on-disk header for this version.
// Chunks start immediately after the header.
func (h ArchiveHeader) HeaderSize() int {
	if h.Version >= 6 {
		return archiveHeaderSizeV6
	}
	if h.Version >= 5 {
		return archiveHeaderSizeV5
	}
	return archiveHeaderSizeV4
}

type chunkHeader struct {
	StartTime         int32
	EndTime           int32
	FrameCount        int32
	EventCount        int32
	UncompressedBytes int32
	CompressedBytes   int32
}

type Sample struct {
	ClientNum   int32
	Health      int32
	Team        int32
	PmType      int32
	PmFlags     int32
	Weaponstate int32
	Weapon      int32  // -1 if sampleSize doesn't match knownSampleSize
	PlayerClass int32  // -1 if not present in this layout
	Origin      [3]float32
	Velocity    [3]float32
	ViewAngles  [3]float32
}

type Event struct {
	ServerTime      int32
	ActorClientNum  int32
	TargetClientNum int32
	Score           int32
	Type            EventType
	MeansOfDeath    int32
	Extra           int32
	Origin          [3]float32
}

type Frame struct {
	ServerTime int32
	Samples    []Sample
}

type Replay struct {
	Header ArchiveHeader
	Frames []Frame
	Events []Event
}

type sampleLayout struct {
	sampleSize      int32
	offWeapon       int
	offOrigin       int
	offVelocity     int
	offViewAngles   int
	offPlayerClass  int // -1 if not present in this layout
}

var knownSampleLayouts = map[int32]sampleLayout{
	// Original layout: sizeof(replaySample_t) == 380, entityState_t == 280.
	380: {
		sampleSize:     380,
		offWeapon:      288,
		offOrigin:      344,
		offVelocity:    356,
		offViewAngles:  368,
		offPlayerClass: -1,
	},
	// Layout with viewlocked/weaponTime/leanf but no playerClass: entityState_t == 288.
	388: {
		sampleSize:     388,
		offWeapon:      296,
		offOrigin:      352,
		offVelocity:    364,
		offViewAngles:  376,
		offPlayerClass: -1,
	},
	// Layout with playerClass added after weaponTime (offset 60), before leanf (offset 64).
	// es starts at 68 (size 288), origin at 356, vel at 368, angles at 380. Total: 392.
	392: {
		sampleSize:     392,
		offWeapon:      300, // 68 + 232 (weapon field within entityState_t)
		offOrigin:      356, // 68 + 288
		offVelocity:    368,
		offViewAngles:  380,
		offPlayerClass: 60,
	},
	// Layout with weapAnim added between weaponTime and playerClass (v7+).
	// playerClass now at 64, leanf at 68, es starts at 72. Total: 396.
	396: {
		sampleSize:     396,
		offWeapon:      304, // 72 + 232
		offOrigin:      360, // 72 + 288
		offVelocity:    372,
		offViewAngles:  384,
		offPlayerClass: 64,
	},
}

func lookupSampleLayout(sampleSize int32) (sampleLayout, bool) {
	layout, ok := knownSampleLayouts[sampleSize]
	return layout, ok
}

func readInt32LE(b []byte, off int) int32 {
	return int32(binary.LittleEndian.Uint32(b[off : off+4]))
}

func readFloat32LE(b []byte, off int) float32 {
	bits := binary.LittleEndian.Uint32(b[off : off+4])
	return math.Float32frombits(bits)
}

func readVec3(b []byte, off int) [3]float32 {
	return [3]float32{
		readFloat32LE(b, off),
		readFloat32LE(b, off+4),
		readFloat32LE(b, off+8),
	}
}

func parseSample(b []byte, layout *sampleLayout) Sample {
	s := Sample{
		ClientNum:   readInt32LE(b, offClientNum),
		Health:      readInt32LE(b, offHealth),
		Team:        readInt32LE(b, offTeam),
		PmType:      readInt32LE(b, offPmType),
		PmFlags:     readInt32LE(b, offPmFlags),
		Weaponstate: readInt32LE(b, offWeaponstate),
		Weapon:      -1,
		PlayerClass: -1,
	}
	if layout != nil {
		s.Weapon = readInt32LE(b, layout.offWeapon)
		s.Origin = readVec3(b, layout.offOrigin)
		s.Velocity = readVec3(b, layout.offVelocity)
		s.ViewAngles = readVec3(b, layout.offViewAngles)
		if layout.offPlayerClass >= 0 {
			s.PlayerClass = readInt32LE(b, layout.offPlayerClass)
		}
	}
	return s
}

func parseEvent(b []byte) Event {
	return Event{
		ServerTime:      readInt32LE(b, offEvServerTime),
		ActorClientNum:  readInt32LE(b, offEvActorClientNum),
		TargetClientNum: readInt32LE(b, offEvTargetClientNum),
		Score:           readInt32LE(b, offEvScore),
		Type:            EventType(readInt32LE(b, offEvType)),
		MeansOfDeath:    readInt32LE(b, offEvMeansOfDeath),
		Extra:           readInt32LE(b, offEvExtra),
		Origin:          readVec3(b, offEvOrigin),
	}
}

func parseArchiveHeader(b []byte) (ArchiveHeader, error) {
	if len(b) < archiveHeaderSizeV4 {
		return ArchiveHeader{}, fmt.Errorf("header too short: %d bytes", len(b))
	}
	h := ArchiveHeader{
		Magic:      readInt32LE(b, 0),
		Version:    readInt32LE(b, 4),
		Codec:      readInt32LE(b, 8),
		RecordMsec: readInt32LE(b, 12),
		ChunkMsec:  readInt32LE(b, 16),
		Gametype:   readInt32LE(b, 20),
		MaxClients: readInt32LE(b, 24),
		SampleSize: readInt32LE(b, 28),
		EventSize:  readInt32LE(b, 32),
		FrameCount: readInt32LE(b, 36),
		EventCount: readInt32LE(b, 40),
	}
	// MapName: 64 bytes at offset 44, null-terminated.
	nameBytes := b[44 : 44+mapNameSize]
	if end := bytes.IndexByte(nameBytes, 0); end >= 0 {
		nameBytes = nameBytes[:end]
	}
	h.MapName = string(nameBytes)

	// Version 5+: player name table immediately after the base 108-byte header.
	if h.Version >= 5 && len(b) >= archiveHeaderSizeV5 {
		off := archiveHeaderSizeV4
		for i := 0; i < maxClientsInHeader; i++ {
			slot := b[off : off+maxNetnameInHeader]
			if end := bytes.IndexByte(slot, 0); end >= 0 {
				slot = slot[:end]
			}
			if len(slot) > 0 {
				h.PlayerNames[i] = string(slot)
			}
			off += maxNetnameInHeader
		}
	}

	// Version 6+: spawn-point name table immediately after player names.
	if h.Version >= 6 && len(b) >= archiveHeaderSizeV6 {
		off := archiveHeaderSizeV5
		for i := 0; i < maxSpawnPoints; i++ {
			slot := b[off : off+spawnPointNameSize]
			if end := bytes.IndexByte(slot, 0); end >= 0 {
				slot = slot[:end]
			}
			if len(slot) > 0 {
				h.SpawnPointNames[i] = string(slot)
			}
			off += spawnPointNameSize
		}
	}
	return h, nil
}

func parseChunkHeader(b []byte) chunkHeader {
	return chunkHeader{
		StartTime:         readInt32LE(b, 0),
		EndTime:           readInt32LE(b, 4),
		FrameCount:        readInt32LE(b, 8),
		EventCount:        readInt32LE(b, 12),
		UncompressedBytes: readInt32LE(b, 16),
		CompressedBytes:   readInt32LE(b, 20),
	}
}

func decompressChunk(compressed []byte, uncompressedSize int32) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("zlib open: %w", err)
	}
	defer r.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("zlib read: %w", err)
	}
	if len(out) != int(uncompressedSize) {
		return nil, fmt.Errorf("decompressed %d bytes, expected %d", len(out), uncompressedSize)
	}
	return out, nil
}

// parseChunkPayload reads the decompressed chunk payload and appends frames/events.
// Payload layout: int32 frameCount, int32 eventCount,
// then for each frame: int32 serverTime, int32 sampleCount, [sampleCount*sampleSize bytes],
// then [eventCount*eventSize bytes].
func parseChunkPayload(payload []byte, sampleSize, eventSize int32, layout *sampleLayout) ([]Frame, []Event, error) {
	if len(payload) < 8 {
		return nil, nil, fmt.Errorf("chunk payload too short")
	}
	frameCount := readInt32LE(payload, 0)
	eventCount := readInt32LE(payload, 4)
	pos := 8

	frames := make([]Frame, 0, frameCount)
	for i := int32(0); i < frameCount; i++ {
		if pos+8 > len(payload) {
			return nil, nil, fmt.Errorf("frame header out of bounds at frame %d", i)
		}
		serverTime := readInt32LE(payload, pos)
		sampleCount := readInt32LE(payload, pos+4)
		pos += 8

		f := Frame{ServerTime: serverTime, Samples: make([]Sample, 0, sampleCount)}
		for j := int32(0); j < sampleCount; j++ {
			end := pos + int(sampleSize)
			if end > len(payload) {
				return nil, nil, fmt.Errorf("sample out of bounds at frame %d sample %d", i, j)
			}
			f.Samples = append(f.Samples, parseSample(payload[pos:end], layout))
			pos = end
		}
		frames = append(frames, f)
	}

	events := make([]Event, 0, eventCount)
	for i := int32(0); i < eventCount; i++ {
		end := pos + int(eventSize)
		if end > len(payload) {
			return nil, nil, fmt.Errorf("event out of bounds at event %d", i)
		}
		events = append(events, parseEvent(payload[pos:end]))
		pos = end
	}

	return frames, events, nil
}

// Parse reads and decodes a .rpl file.
func Parse(path string) (*Replay, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	header, err := parseArchiveHeader(data)
	if err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if header.Magic != ArchiveMagic {
		return nil, fmt.Errorf("bad magic: 0x%08X", header.Magic)
	}
	if header.Version < ArchiveVersion || header.Version > ArchiveVersionCurrent {
		return nil, fmt.Errorf("unsupported version: %d", header.Version)
	}

	layout, hasLayout := lookupSampleLayout(header.SampleSize)
	var layoutPtr *sampleLayout
	if hasLayout {
		layoutPtr = &layout
	}

	replay := &Replay{Header: header}
	pos := header.HeaderSize()

	for pos < len(data) {
		if pos+chunkHeaderSize > len(data) {
			break
		}
		ch := parseChunkHeader(data[pos : pos+chunkHeaderSize])
		pos += chunkHeaderSize

		end := pos + int(ch.CompressedBytes)
		if end > len(data) {
			return nil, fmt.Errorf("chunk compressed data out of bounds")
		}
		compressed := data[pos:end]
		pos = end

		payload, err := decompressChunk(compressed, ch.UncompressedBytes)
		if err != nil {
			return nil, fmt.Errorf("decompress chunk: %w", err)
		}

		frames, events, err := parseChunkPayload(payload, header.SampleSize, header.EventSize, layoutPtr)
		if err != nil {
			return nil, fmt.Errorf("parse chunk payload: %w", err)
		}

		replay.Frames = append(replay.Frames, frames...)
		replay.Events = append(replay.Events, events...)
	}

	return replay, nil
}
