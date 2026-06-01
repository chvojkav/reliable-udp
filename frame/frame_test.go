package frame_test

import (
	"bytes"
	"testing"

	"github.com/chvojkav/reliable-udp/frame"
)

// golden is a fully-specified frame whose raw bytes pin every field offset and
// endianness. Any codec reimplemented from SPEC.md §7 must produce and parse
// this byte sequence identically.
//
// Fields:
//
//	SeqNum     = 0x0102_0304  (bytes 0–3)
//	AckNum     = 0x0506_0708  (bytes 4–7)
//	Window     = 0x0910       (bytes 8–9)
//	Flags      = FlagSYN|FlagACK = 0x03  (byte 10)
//	DataOffset = 16           (byte 11)
//	PayloadLen = 5            (bytes 12–13)
//	Reserved   = 0            (bytes 14–15)
//	Payload    = "hello"
var goldenBytes = []byte{
	0x01, 0x02, 0x03, 0x04, // SeqNum  big-endian
	0x05, 0x06, 0x07, 0x08, // AckNum  big-endian
	0x09, 0x10, // Window  big-endian
	0x03,       // Flags   SYN|ACK
	0x10,       // DataOffset = 16
	0x00, 0x05, // PayloadLen big-endian
	0x00, 0x00, // Reserved
	'h', 'e', 'l', 'l', 'o', // payload
}

var goldenHeader = frame.Header{
	SeqNum:     0x01020304,
	AckNum:     0x05060708,
	Window:     0x0910,
	Flags:      frame.FlagSYN | frame.FlagACK,
	DataOffset: 16,
	PayloadLen: 5,
	Reserved:   0,
}

// TestGoldenMarshal checks that Marshal produces the exact golden byte sequence.
func TestGoldenMarshal(t *testing.T) {
	got, err := frame.Marshal(goldenHeader, nil, []byte("hello"))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(got, goldenBytes) {
		t.Errorf("Marshal output mismatch\ngot:  %x\nwant: %x", got, goldenBytes)
	}
}

// TestGoldenUnmarshal checks that Unmarshal recovers the exact golden header and payload.
func TestGoldenUnmarshal(t *testing.T) {
	h, opts, payload, err := frame.Unmarshal(goldenBytes)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if h != goldenHeader {
		t.Errorf("header mismatch\ngot:  %+v\nwant: %+v", h, goldenHeader)
	}
	if len(opts) != 0 {
		t.Errorf("expected no options, got %d bytes", len(opts))
	}
	if !bytes.Equal(payload, []byte("hello")) {
		t.Errorf("payload mismatch: got %q", payload)
	}
}

// TestRoundTrip exercises every exported field and every flag bit individually
// and in combinations.
func TestRoundTrip(t *testing.T) {
	allFlags := []struct {
		name string
		flag uint8
	}{
		{"SYN", frame.FlagSYN},
		{"ACK", frame.FlagACK},
		{"FIN", frame.FlagFIN},
		{"RST", frame.FlagRST},
		{"SYN|ACK", frame.FlagSYN | frame.FlagACK},
		{"SYN|FIN", frame.FlagSYN | frame.FlagFIN},
		{"ACK|FIN", frame.FlagACK | frame.FlagFIN},
		{"SYN|ACK|FIN|RST", frame.FlagSYN | frame.FlagACK | frame.FlagFIN | frame.FlagRST},
		{"none", 0},
	}

	for _, tc := range allFlags {
		t.Run("flags="+tc.name, func(t *testing.T) {
			h := frame.Header{
				SeqNum: 0xDEAD_BEEF,
				AckNum: 0xCAFE_BABE,
				Window: 0xFFFF,
				Flags:  tc.flag,
			}
			payload := []byte("round-trip")

			raw, err := frame.Marshal(h, nil, payload)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			got, opts, gotPayload, err := frame.Unmarshal(raw)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.Flags != tc.flag {
				t.Errorf("Flags: got 0x%02x want 0x%02x", got.Flags, tc.flag)
			}
			if got.SeqNum != h.SeqNum {
				t.Errorf("SeqNum: got %d want %d", got.SeqNum, h.SeqNum)
			}
			if got.AckNum != h.AckNum {
				t.Errorf("AckNum: got %d want %d", got.AckNum, h.AckNum)
			}
			if got.Window != h.Window {
				t.Errorf("Window: got %d want %d", got.Window, h.Window)
			}
			if len(opts) != 0 {
				t.Errorf("unexpected options: %d bytes", len(opts))
			}
			if !bytes.Equal(gotPayload, payload) {
				t.Errorf("payload: got %q want %q", gotPayload, payload)
			}
		})
	}
}

// TestOptionsPresent verifies DataOffset>16 path: options are sliced out
// correctly and payload follows immediately after.
func TestOptionsPresent(t *testing.T) {
	h := frame.Header{
		SeqNum: 1,
		AckNum: 2,
		Window: 1024,
		Flags:  frame.FlagACK,
	}
	opts := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	payload := []byte("with-options")

	raw, err := frame.Marshal(h, opts, payload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, gotOpts, gotPayload, err := frame.Unmarshal(raw)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.DataOffset != uint8(frame.HeaderSize+len(opts)) {
		t.Errorf("DataOffset: got %d want %d", got.DataOffset, frame.HeaderSize+len(opts))
	}
	if !bytes.Equal(gotOpts, opts) {
		t.Errorf("options: got %x want %x", gotOpts, opts)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Errorf("payload: got %q want %q", gotPayload, payload)
	}
}

// TestEmptyPayload verifies a header-only frame (PayloadLen==0).
func TestEmptyPayload(t *testing.T) {
	h := frame.Header{
		SeqNum: 42,
		Flags:  frame.FlagSYN,
	}

	raw, err := frame.Marshal(h, nil, nil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if len(raw) != frame.HeaderSize {
		t.Errorf("expected %d bytes, got %d", frame.HeaderSize, len(raw))
	}

	got, opts, payload, err := frame.Unmarshal(raw)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.PayloadLen != 0 {
		t.Errorf("PayloadLen: got %d want 0", got.PayloadLen)
	}
	if len(opts) != 0 || len(payload) != 0 {
		t.Errorf("expected empty options and payload")
	}
}

// TestHasSet exercises the Header.Has and Header.Set helpers.
func TestHasSet(t *testing.T) {
	var h frame.Header

	if h.Has(frame.FlagSYN) {
		t.Error("fresh header should not have FlagSYN")
	}

	h.Set(frame.FlagSYN)
	if !h.Has(frame.FlagSYN) {
		t.Error("FlagSYN should be set after Set(FlagSYN)")
	}
	if h.Has(frame.FlagACK) {
		t.Error("FlagACK should not be set")
	}

	h.Set(frame.FlagACK | frame.FlagFIN)
	if !h.Has(frame.FlagSYN | frame.FlagACK | frame.FlagFIN) {
		t.Error("SYN|ACK|FIN should all be set")
	}
}

// TestUnmarshalErrors verifies every validation error path.
func TestUnmarshalErrors(t *testing.T) {
	validFrame, err := frame.Marshal(frame.Header{SeqNum: 1, Flags: frame.FlagSYN}, nil, []byte("data"))
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	cases := []struct {
		name    string
		input   []byte
		wantErr error
	}{
		{
			name:    "empty buffer",
			input:   []byte{},
			wantErr: frame.ErrTooShort,
		},
		{
			name:    "15 bytes (one short)",
			input:   validFrame[:15],
			wantErr: frame.ErrTooShort,
		},
		{
			name: "DataOffset=15 (<16)",
			input: func() []byte {
				b := bytes.Clone(validFrame)
				b[11] = 15
				return b
			}(),
			wantErr: frame.ErrDataOffsetSmall,
		},
		{
			name: "DataOffset=0",
			input: func() []byte {
				b := bytes.Clone(validFrame)
				b[11] = 0
				return b
			}(),
			wantErr: frame.ErrDataOffsetSmall,
		},
		{
			name: "DataOffset beyond buffer",
			input: func() []byte {
				b := bytes.Clone(validFrame)
				b[11] = byte(len(b) + 1)
				return b
			}(),
			wantErr: frame.ErrDataOffsetLarge,
		},
		{
			name: "PayloadLen beyond buffer",
			input: func() []byte {
				b := bytes.Clone(validFrame)
				// DataOffset is 16, so set PayloadLen to way more than remaining bytes
				b[12] = 0xFF
				b[13] = 0xFF
				return b
			}(),
			wantErr: frame.ErrPayloadLarge,
		},
		{
			name: "nonzero Reserved",
			input: func() []byte {
				b := bytes.Clone(validFrame)
				b[14] = 0x00
				b[15] = 0x01
				return b
			}(),
			wantErr: frame.ErrNonzeroReserved,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := frame.Unmarshal(tc.input)
			if err != tc.wantErr {
				t.Errorf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestMarshalRejectsNonzeroReserved verifies Marshal rejects a non-zero Reserved field.
func TestMarshalRejectsNonzeroReserved(t *testing.T) {
	h := frame.Header{Reserved: 1}
	_, err := frame.Marshal(h, nil, nil)
	if err != frame.ErrNonzeroReserved {
		t.Errorf("got %v, want ErrNonzeroReserved", err)
	}
}
