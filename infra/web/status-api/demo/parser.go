package demo

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Server-to-client opcodes (qcommon.h svc_ops_e).
const (
	svcNop           = 1
	svcGamestate     = 2
	svcConfigstring  = 3
	svcBaseline      = 4
	svcServerCommand = 5
	svcDownload      = 6
	svcSnapshot      = 7
	svcEOF           = 8
)

const (
	gentityBits    = 10
	maxGentities   = 1 << gentityBits
	maxConfigstrs  = 2048
	maxStringChars = 1024
	bigInfoString  = 8192
	packetBackup   = 32
	maxMessageLen  = 1 << 17 // sanity bound on one demo message
	floatIntBits   = 13
	floatIntBias   = 1 << (floatIntBits - 1)
	maxStateFields = 96
)

// EntityState holds one delta-decoded entityState_t as raw field words.
// Floats are stored as their IEEE bits; use Float to read them.
type EntityState struct {
	Number int
	F      [maxStateFields]uint32
}

// PlayerState holds a delta-decoded playerState_t.
type PlayerState struct {
	F          [maxStateFields]uint32
	Stats      [16]int32
	Persistant [16]int32
	Holdable   [16]int32
	Powerups   [16]int32
	Ammo       [64]int32
	AmmoClip   [64]int32
}

// Snapshot is one server frame as seen by the recording client.
type Snapshot struct {
	MessageNum int
	DeltaNum   int
	ServerTime int
	Flags      int
	PS         PlayerState
	Entities   []EntityState // sorted by Number
}

// Command is a server command string (chat, prints, configstring updates).
type Command struct {
	ServerTime int // time of the snapshot that followed it in the same message
	Seq        int
	Text       string
}

// Demo is a parsed client demo.
type Demo struct {
	Protocol      int
	Configstrings map[int]string
	ClientNum     int
	ChecksumFeed  int
	Baselines     map[int]EntityState
	Frames        []Snapshot
	Commands      []Command
	Layout        *Layout
}

// Layout is the per-protocol field description used to decode state deltas.
type Layout struct {
	Entity []netField
	Player []netField
	index  map[string]int
	pindex map[string]int
}

func newLayout(e, p []netField) *Layout {
	l := &Layout{Entity: e, Player: p, index: map[string]int{}, pindex: map[string]int{}}
	for i, f := range e {
		l.index[f.name] = i
	}
	for i, f := range p {
		l.pindex[f.name] = i
	}
	return l
}

// EntityIdx returns the field index for an entityState name, or -1.
func (l *Layout) EntityIdx(name string) int {
	if i, ok := l.index[name]; ok {
		return i
	}
	return -1
}

// PlayerIdx returns the field index for a playerState name, or -1.
func (l *Layout) PlayerIdx(name string) int {
	if i, ok := l.pindex[name]; ok {
		return i
	}
	return -1
}

// LayoutFor returns the layout for a protocol version, or nil if unknown.
func LayoutFor(protocol int) *Layout {
	switch protocol {
	case 57, 59, 60, 61:
		return newLayout(entityFields61, playerFields61)
	}
	return nil
}

// Float interprets a raw field word as float32.
func Float(w uint32) float64 { return float64(math.Float32frombits(w)) }

// ErrTruncated marks a demo that ended (or broke) before the end marker.
var ErrTruncated = errors.New("demo: truncated or corrupt message")

// Parse decodes a whole demo.  protocol selects the netfield tables.  On a
// mid-stream error the frames decoded so far are returned with the error.
func Parse(data []byte, protocol int) (*Demo, error) {
	lay := LayoutFor(protocol)
	if lay == nil {
		return nil, fmt.Errorf("demo: unsupported protocol %d", protocol)
	}
	d := &Demo{
		Protocol:      protocol,
		Configstrings: map[int]string{},
		Baselines:     map[int]EntityState{},
		Layout:        lay,
	}
	p := &parser{d: d, lay: lay}
	pos := 0
	for {
		if pos+8 > len(data) {
			return d, ErrTruncated
		}
		seq := int(int32(binary.LittleEndian.Uint32(data[pos:])))
		n := int(int32(binary.LittleEndian.Uint32(data[pos+4:])))
		pos += 8
		if seq == -1 && n == -1 {
			return d, nil
		}
		if n < 0 || n > maxMessageLen || pos+n > len(data) {
			return d, ErrTruncated
		}
		if err := p.message(seq, data[pos:pos+n]); err != nil {
			return d, err
		}
		pos += n
	}
}

type parser struct {
	d     *Demo
	lay   *Layout
	snaps [packetBackup]*Snapshot
	// commands read in the current message, awaiting the snapshot's time
	pending []int
}

func (p *parser) message(seq int, buf []byte) error {
	m := NewMsg(buf)
	m.ReadLong() // reliable acknowledge
	p.pending = p.pending[:0]
	for {
		if m.Err != nil {
			return m.Err
		}
		cmd := m.ReadU8()
		if m.Err != nil {
			return m.Err
		}
		switch cmd {
		case svcEOF:
			return nil
		case svcNop:
		case svcServerCommand:
			cseq := m.ReadLong()
			s := m.ReadString(maxStringChars)
			if m.Err != nil {
				return m.Err
			}
			p.d.Commands = append(p.d.Commands, Command{ServerTime: -1, Seq: cseq, Text: s})
			p.pending = append(p.pending, len(p.d.Commands)-1)
			p.applyCommand(s)
		case svcGamestate:
			if err := p.gamestate(m); err != nil {
				return err
			}
		case svcSnapshot:
			if err := p.snapshot(m, seq); err != nil {
				return err
			}
		default:
			return fmt.Errorf("demo: unexpected svc opcode %d in message %d", cmd, seq)
		}
	}
}

// applyCommand folds configstring updates into the running table.
func (p *parser) applyCommand(s string) {
	f := strings.SplitN(s, " ", 3)
	if len(f) == 3 && f[0] == "cs" {
		var n int
		if _, err := fmt.Sscanf(f[1], "%d", &n); err == nil && n >= 0 && n < maxConfigstrs {
			p.d.Configstrings[n] = strings.Trim(strings.TrimSpace(f[2]), "\"")
		}
	}
}

func (p *parser) gamestate(m *Msg) error {
	m.ReadLong() // server command sequence
	for {
		cmd := m.ReadU8()
		if m.Err != nil {
			return m.Err
		}
		if cmd == svcEOF {
			break
		}
		switch cmd {
		case svcConfigstring:
			i := m.ReadShort()
			s := m.ReadString(bigInfoString)
			if m.Err != nil {
				return m.Err
			}
			if i < 0 || i >= maxConfigstrs {
				return fmt.Errorf("demo: configstring %d out of range", i)
			}
			p.d.Configstrings[i] = s
		case svcBaseline:
			num := m.ReadBits(gentityBits)
			var base, es EntityState
			if !p.readDeltaEntity(m, &base, &es, num) {
				return m.Err
			}
			if m.Err != nil {
				return m.Err
			}
			p.d.Baselines[num] = es
		default:
			return fmt.Errorf("demo: bad gamestate command %d", cmd)
		}
	}
	p.d.ClientNum = m.ReadLong()
	p.d.ChecksumFeed = m.ReadLong()
	// a new gamestate invalidates all delta history
	p.snaps = [packetBackup]*Snapshot{}
	return m.Err
}

func (p *parser) snapshot(m *Msg, messageNum int) error {
	snap := &Snapshot{MessageNum: messageNum}
	snap.ServerTime = m.ReadLong()
	deltaNum := m.ReadU8()
	if deltaNum == 0 {
		snap.DeltaNum = -1
	} else {
		snap.DeltaNum = messageNum - deltaNum
	}
	snap.Flags = m.ReadU8()

	var old *Snapshot
	if snap.DeltaNum > 0 {
		old = p.snaps[snap.DeltaNum&(packetBackup-1)]
		if old != nil && old.MessageNum != snap.DeltaNum {
			old = nil
		}
	}

	areaLen := m.ReadU8()
	if areaLen > 32 {
		return fmt.Errorf("demo: invalid areamask size %d", areaLen)
	}
	for i := 0; i < areaLen; i++ {
		m.ReadU8()
	}

	var oldPS *PlayerState
	if old != nil {
		oldPS = &old.PS
	}
	if !p.readDeltaPlayerstate(m, oldPS, &snap.PS) {
		return m.Err
	}

	if err := p.packetEntities(m, old, snap); err != nil {
		return err
	}
	if m.Err != nil {
		return m.Err
	}

	// a delta against a frame we no longer hold cannot be reconstructed;
	// the client drops it, and so do we (the bits were still consumed)
	if snap.DeltaNum > 0 && old == nil {
		return nil
	}

	for _, ci := range p.pending {
		p.d.Commands[ci].ServerTime = snap.ServerTime
	}
	p.pending = p.pending[:0]
	p.snaps[messageNum&(packetBackup-1)] = snap
	p.d.Frames = append(p.d.Frames, *snap)
	return nil
}

func (p *parser) packetEntities(m *Msg, old, snap *Snapshot) error {
	oldIdx := 0
	oldNum := 99999
	var oldState *EntityState
	next := func() {
		oldIdx++
		if old == nil || oldIdx >= len(old.Entities) {
			oldNum = 99999
			oldState = nil
		} else {
			oldState = &old.Entities[oldIdx]
			oldNum = oldState.Number
		}
	}
	if old != nil && len(old.Entities) > 0 {
		oldState = &old.Entities[0]
		oldNum = oldState.Number
	}
	add := func(es EntityState) { snap.Entities = append(snap.Entities, es) }

	for {
		newNum := m.ReadBits(gentityBits)
		if m.Err != nil {
			return m.Err
		}
		if newNum == maxGentities-1 {
			break
		}
		for oldNum < newNum {
			add(*oldState) // unchanged
			next()
		}
		switch {
		case oldNum == newNum:
			var es EntityState
			if !p.readDeltaEntity(m, oldState, &es, newNum) {
				return m.Err
			}
			if es.Number != maxGentities-1 {
				add(es)
			}
			next()
		case oldNum > newNum:
			base := p.d.Baselines[newNum]
			var es EntityState
			if !p.readDeltaEntity(m, &base, &es, newNum) {
				return m.Err
			}
			if es.Number != maxGentities-1 {
				add(es)
			}
		}
	}
	for oldNum != 99999 {
		add(*oldState)
		next()
	}
	return nil
}

// readDeltaEntity mirrors MSG_ReadDeltaEntity.  Returns false on a read error.
func (p *parser) readDeltaEntity(m *Msg, from, to *EntityState, number int) bool {
	if number < 0 || number >= maxGentities {
		m.Err = fmt.Errorf("demo: bad delta entity number %d", number)
		return false
	}
	if m.ReadBits(1) == 1 { // removed
		*to = EntityState{Number: maxGentities - 1}
		return m.Err == nil
	}
	if m.ReadBits(1) == 0 { // no delta
		*to = *from
		to.Number = number
		return m.Err == nil
	}
	fields := p.lay.Entity
	lc := m.ReadU8()
	if lc > len(fields) {
		m.Err = fmt.Errorf("demo: invalid entityState field count %d", lc)
		return false
	}
	*to = *from
	to.Number = number
	for i := 0; i < lc; i++ {
		if m.ReadBits(1) == 0 {
			continue
		}
		f := fields[i]
		if f.bits == 0 {
			if m.ReadBits(1) == 0 {
				to.F[i] = 0
			} else if m.ReadBits(1) == 0 {
				to.F[i] = math.Float32bits(float32(m.ReadBits(floatIntBits) - floatIntBias))
			} else {
				to.F[i] = uint32(m.ReadBits(32))
			}
		} else if m.ReadBits(1) == 0 {
			to.F[i] = 0
		} else {
			to.F[i] = uint32(m.ReadBits(f.bits))
		}
		if m.Err != nil {
			return false
		}
	}
	return m.Err == nil
}

// readDeltaPlayerstate mirrors MSG_ReadDeltaPlayerstate.
func (p *parser) readDeltaPlayerstate(m *Msg, from, to *PlayerState) bool {
	if from == nil {
		from = &PlayerState{}
	}
	*to = *from
	fields := p.lay.Player
	lc := m.ReadU8()
	if lc > len(fields) {
		m.Err = fmt.Errorf("demo: invalid playerState field count %d", lc)
		return false
	}
	for i := 0; i < lc; i++ {
		if m.ReadBits(1) == 0 {
			continue
		}
		f := fields[i]
		if f.bits == 0 {
			if m.ReadBits(1) == 0 {
				to.F[i] = math.Float32bits(float32(m.ReadBits(floatIntBits) - floatIntBias))
			} else {
				to.F[i] = uint32(m.ReadBits(32))
			}
		} else {
			to.F[i] = uint32(m.ReadBits(f.bits))
		}
		if m.Err != nil {
			return false
		}
	}

	if m.ReadBits(1) == 1 {
		if m.ReadBits(1) == 1 { // stats
			bits := m.ReadBits(16)
			for i := 0; i < 16; i++ {
				if bits&(1<<uint(i)) != 0 {
					to.Stats[i] = int32(m.ReadShort())
				}
			}
		}
		if m.ReadBits(1) == 1 { // persistant
			bits := m.ReadBits(16)
			for i := 0; i < 16; i++ {
				if bits&(1<<uint(i)) != 0 {
					to.Persistant[i] = int32(m.ReadShort())
				}
			}
		}
		if m.ReadBits(1) == 1 { // holdable
			bits := m.ReadShort()
			for i := 0; i < 16; i++ {
				if bits&(1<<uint(i)) != 0 {
					to.Holdable[i] = int32(m.ReadShort())
				}
			}
		}
		if m.ReadBits(1) == 1 { // powerups
			bits := m.ReadBits(16)
			for i := 0; i < 16; i++ {
				if bits&(1<<uint(i)) != 0 {
					to.Powerups[i] = int32(m.ReadLong())
				}
			}
		}
	}

	if m.ReadBits(1) == 1 { // ammo
		for j := 0; j < 4; j++ {
			if m.ReadBits(1) == 1 {
				bits := m.ReadShort()
				for i := 0; i < 16; i++ {
					if bits&(1<<uint(i)) != 0 {
						to.Ammo[i+j*16] = int32(m.ReadShort())
					}
				}
			}
		}
	}
	for j := 0; j < 4; j++ { // ammo in clip
		if m.ReadBits(1) == 1 {
			bits := m.ReadShort()
			for i := 0; i < 16; i++ {
				if bits&(1<<uint(i)) != 0 {
					to.AmmoClip[i+j*16] = int32(m.ReadShort())
				}
			}
		}
	}
	return m.Err == nil
}
