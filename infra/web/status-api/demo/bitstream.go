package demo

import "errors"

// ErrOverflow is returned (via Msg.Err) when a read runs past the message.
var ErrOverflow = errors.New("demo: read past end of message")

// Msg reads the Huffman/bit-packed server messages (iortcw MSG_Read*).
// Reads never panic: on overrun they return 0 and latch Err, so callers can
// parse a whole structure and check Err once.  Demo files are untrusted.
type Msg struct {
	data []byte
	bit  int // bit offset into data
	Err  error
}

func NewMsg(data []byte) *Msg { return &Msg{data: data} }

func (m *Msg) bitsLeft() int { return len(m.data)*8 - m.bit }

// Done reports whether the whole message has been consumed or errored.
func (m *Msg) Done() bool { return m.Err != nil || m.bitsLeft() <= 0 }

func (m *Msg) getBit() int {
	t := int(m.data[m.bit>>3]>>(uint(m.bit)&7)) & 1
	m.bit++
	return t
}

func (m *Msg) readHuffByte() int {
	node := msgTree
	for node != nil && node.symbol == internalNode {
		if m.bit >= len(m.data)*8 {
			m.Err = ErrOverflow
			return 0
		}
		if m.getBit() == 1 {
			node = node.right
		} else {
			node = node.left
		}
	}
	if node == nil {
		m.Err = ErrOverflow
		return 0
	}
	return node.symbol
}

// ReadBits mirrors MSG_ReadBits for the non-OOB bitstream; negative bits
// means a signed value.
func (m *Msg) ReadBits(bits int) int {
	if m.Err != nil {
		return 0
	}
	signed := false
	if bits < 0 {
		bits = -bits
		signed = true
	}
	value := uint32(0)
	nbits := 0
	if bits&7 != 0 {
		nbits = bits & 7
		if m.bit+nbits > len(m.data)*8 {
			m.Err = ErrOverflow
			return 0
		}
		for i := 0; i < nbits; i++ {
			value |= uint32(m.getBit()) << uint(i)
		}
		bits -= nbits
	}
	for i := 0; i < bits; i += 8 {
		b := m.readHuffByte()
		if m.Err != nil {
			return 0
		}
		value |= uint32(b) << uint(i+nbits)
	}
	total := bits + nbits
	if signed && total > 0 && total < 32 {
		if value&(1<<uint(total-1)) != 0 {
			value |= ^uint32(0) << uint(total)
		}
	}
	return int(int32(value))
}

func (m *Msg) ReadU8() int    { return m.ReadBits(8) & 0xff }
func (m *Msg) ReadShort() int { return int(int16(m.ReadBits(16))) }
func (m *Msg) ReadLong() int  { return m.ReadBits(32) }

// ReadString reads a NUL-terminated string, applying the engine's
// sanitising ('%' and bytes >127 become '.').  max bounds the length.
func (m *Msg) ReadString(max int) string {
	var buf []byte
	for {
		if m.Err != nil || m.bitsLeft() <= 0 {
			if m.bitsLeft() <= 0 && m.Err == nil {
				m.Err = ErrOverflow
			}
			break
		}
		c := m.ReadU8()
		if m.Err != nil || c == 0 {
			break
		}
		if c == '%' || c > 127 {
			c = '.'
		}
		if len(buf) >= max-1 {
			break
		}
		buf = append(buf, byte(c))
	}
	return string(buf)
}
