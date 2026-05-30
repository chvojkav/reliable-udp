// Package frame implements the wire-format codec for the reliable-UDP base
// header (SPEC.md §7). It is the single source of truth that the Wireshark
// dissector (wireshark/reliable_udp.lua) mirrors — keep field offsets here
// and there identical.
//
// All multi-byte fields are big-endian (network byte order) per SPEC.md §7.
package frame

import (
	"encoding/binary"
	"errors"
)

// HeaderSize is the fixed base header length in bytes (SPEC.md §7.1).
const HeaderSize = 16

// Flag bits for the Flags byte (SPEC.md §7.3).
const (
	FlagSYN uint8 = 1 << 0 // bit 0 — synchronise sequence numbers; opens the connection
	FlagACK uint8 = 1 << 1 // bit 1 — AckNum field is valid
	FlagFIN uint8 = 1 << 2 // bit 2 — sender is finished; begins orderly close
	FlagRST uint8 = 1 << 3 // bit 3 — reserved for abnormal close (stretch goal)
)

// Sentinel errors returned by Unmarshal.
var (
	ErrTooShort        = errors.New("frame: buffer shorter than 16-byte base header")
	ErrDataOffsetSmall = errors.New("frame: DataOffset < 16 (must be at least header size)")
	ErrDataOffsetLarge = errors.New("frame: DataOffset extends beyond buffer")
	ErrPayloadLarge    = errors.New("frame: PayloadLen extends beyond buffer")
	ErrNonzeroReserved = errors.New("frame: Reserved field must be zero in v1")
)

// Header is the decoded representation of the 16-byte base header (SPEC.md §7.1).
//
// SeqNum and AckNum count bytes, not packets (byte-stream semantics, like TCP).
// AckNum is meaningful only when FlagACK is set in Flags; the codec carries the
// field unconditionally and does not enforce this constraint.
//
// Note for transport implementors: SYN and FIN each consume exactly one byte of
// sequence space (SPEC.md §7.4), even though they carry no payload. Sequence
// arithmetic is the transport's concern, not the codec's.
type Header struct {
	SeqNum     uint32
	AckNum     uint32
	Window     uint16
	Flags      uint8
	DataOffset uint8  // header length in bytes; 16 in v1, >16 when options are present
	PayloadLen uint16
	Reserved   uint16 // must be zero in v1
}

// Has reports whether all bits in flag are set in h.Flags.
func (h Header) Has(flag uint8) bool {
	return h.Flags&flag == flag
}

// Set sets all bits in flag in h.Flags.
func (h *Header) Set(flag uint8) {
	h.Flags |= flag
}

// Marshal serialises h, options, and payload into a single byte slice.
//
// It sets h.DataOffset = HeaderSize + len(options) and h.PayloadLen =
// len(payload), overriding whatever the caller placed in those fields.
// It returns ErrNonzeroReserved if h.Reserved != 0.
func Marshal(h Header, options []byte, payload []byte) ([]byte, error) {
	if h.Reserved != 0 {
		return nil, ErrNonzeroReserved
	}

	h.DataOffset = uint8(HeaderSize + len(options))
	h.PayloadLen = uint16(len(payload))

	total := int(h.DataOffset) + len(payload)
	buf := make([]byte, total)

	binary.BigEndian.PutUint32(buf[0:4], h.SeqNum)
	binary.BigEndian.PutUint32(buf[4:8], h.AckNum)
	binary.BigEndian.PutUint16(buf[8:10], h.Window)
	buf[10] = h.Flags
	buf[11] = h.DataOffset
	binary.BigEndian.PutUint16(buf[12:14], h.PayloadLen)
	binary.BigEndian.PutUint16(buf[14:16], h.Reserved)

	copy(buf[HeaderSize:h.DataOffset], options)
	copy(buf[h.DataOffset:], payload)

	return buf, nil
}

// Unmarshal decodes a frame from b.
//
// It validates:
//   - len(b) >= 16
//   - DataOffset >= 16
//   - DataOffset <= len(b)
//   - DataOffset + PayloadLen <= len(b)
//   - Reserved == 0
//
// options and payload are slices of b (zero-copy). Callers that need to retain
// them beyond b's lifetime must copy.
func Unmarshal(b []byte) (h Header, options []byte, payload []byte, err error) {
	if len(b) < HeaderSize {
		return Header{}, nil, nil, ErrTooShort
	}

	h.SeqNum = binary.BigEndian.Uint32(b[0:4])
	h.AckNum = binary.BigEndian.Uint32(b[4:8])
	h.Window = binary.BigEndian.Uint16(b[8:10])
	h.Flags = b[10]
	h.DataOffset = b[11]
	h.PayloadLen = binary.BigEndian.Uint16(b[12:14])
	h.Reserved = binary.BigEndian.Uint16(b[14:16])

	if h.DataOffset < HeaderSize {
		return Header{}, nil, nil, ErrDataOffsetSmall
	}
	if int(h.DataOffset) > len(b) {
		return Header{}, nil, nil, ErrDataOffsetLarge
	}

	end := int(h.DataOffset) + int(h.PayloadLen)
	if end > len(b) {
		return Header{}, nil, nil, ErrPayloadLarge
	}

	if h.Reserved != 0 {
		return Header{}, nil, nil, ErrNonzeroReserved
	}

	options = b[HeaderSize:h.DataOffset]
	payload = b[h.DataOffset:end]
	return h, options, payload, nil
}
