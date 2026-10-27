// SPDX-License-Identifier: Apache-2.0
// Wire behavior adapted from opencodex; see LICENSE.upstream.
package cursor

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"math"
	"unicode/utf8"
)

const (
	MaxFrameBytes    = 16 << 20
	MaxRequestBytes  = 2 << 20
	MaxBlobBytes     = 16 << 20
	MaxStateBytes    = 16 << 20
	MaxToolBytes     = 120000
	MaxTools         = 330
	MaxToolCalls     = 256
	MaxArgumentBytes = 1 << 20
	MaxRootBytes     = 512 << 10
	MaxRoots         = 192
)

type Frame struct {
	Flags   byte
	Payload []byte
}

// ReadFrame bounds both compressed input and expanded output before allocation.
// The host must close the reader when its request context is canceled.
func ReadFrame(r io.Reader) (Frame, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}
	if header[0] & ^byte(3) != 0 {
		return Frame{}, protocolError("cursor_invalid_frame_flags")
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n > MaxFrameBytes {
		return Frame{}, protocolError("cursor_frame_too_large")
	}
	p := make([]byte, int(n))
	if _, err := io.ReadFull(r, p); err != nil {
		return Frame{}, err
	}
	if header[0]&1 != 0 {
		z, err := gzip.NewReader(bytes.NewReader(p))
		if err != nil {
			return Frame{}, protocolError("cursor_invalid_compression")
		}
		p, err = io.ReadAll(io.LimitReader(z, MaxFrameBytes+1))
		closeErr := z.Close()
		if err != nil || closeErr != nil {
			return Frame{}, protocolError("cursor_invalid_compression")
		}
		if len(p) > MaxFrameBytes {
			return Frame{}, protocolError("cursor_frame_too_large")
		}
	}
	return Frame{Flags: header[0], Payload: p}, nil
}

func EncodeFrame(f Frame) ([]byte, error) {
	// Compression is decoded by ReadFrame. Encoders emit uncompressed messages.
	if f.Flags != 0 && f.Flags != 2 {
		return nil, protocolError("cursor_invalid_frame_flags")
	}
	if len(f.Payload) > MaxFrameBytes {
		return nil, protocolError("cursor_frame_too_large")
	}
	b := make([]byte, 5+len(f.Payload))
	b[0] = f.Flags
	binary.BigEndian.PutUint32(b[1:5], uint32(len(f.Payload)))
	copy(b[5:], f.Payload)
	return b, nil
}

// This small schema-specific reader skips unknown fields but rejects malformed
// tags, lengths, groups and overflow. Nested messages are decoded explicitly.
type field struct {
	number int
	wire   byte
	data   []byte
	value  uint64
}
type message []field

func parse(b []byte) (message, error) {
	var out message
	for len(b) > 0 {
		if len(out) >= 65536 {
			return nil, protocolError("cursor_too_many_fields")
		}
		tag, n := binary.Uvarint(b)
		if n <= 0 || tag>>3 == 0 || tag>>3 > (1<<29)-1 {
			return nil, protocolError("cursor_invalid_protobuf")
		}
		b = b[n:]
		f := field{number: int(tag >> 3), wire: byte(tag & 7)}
		switch f.wire {
		case 0:
			f.value, n = binary.Uvarint(b)
			if n <= 0 {
				return nil, protocolError("cursor_invalid_protobuf")
			}
			b = b[n:]
		case 1, 5:
			n = 8
			if f.wire == 5 {
				n = 4
			}
			if len(b) < n {
				return nil, protocolError("cursor_invalid_protobuf")
			}
			f.data, b = b[:n], b[n:]
		case 2:
			v, count := binary.Uvarint(b)
			if count <= 0 || v > uint64(len(b)-count) {
				return nil, protocolError("cursor_invalid_protobuf")
			}
			b = b[count:]
			f.data, b = b[:int(v)], b[int(v):]
		default:
			return nil, protocolError("cursor_invalid_protobuf")
		}
		out = append(out, f)
	}
	return out, nil
}
func (m message) get(n int) field {
	for i := len(m) - 1; i >= 0; i-- {
		if m[i].number == n {
			return m[i]
		}
	}
	return field{}
}
func (m message) sub(n int) (message, error) {
	f := m.get(n)
	if f.number != 0 && f.wire != 2 {
		return nil, protocolError("cursor_invalid_protobuf")
	}
	return parse(f.data)
}
func (m message) str(n int) string { return string(m.get(n).data) }
func pb(n int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(n<<3|2))
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}
func ps(n int, s string) []byte { return pb(n, []byte(s)) }
func pv(n int, v uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3)), v)
}
func join(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// Cursor MCP arguments are map<string, bytes>, each value a protobuf Value.
func decodeValue(b []byte, depth int) (any, error) {
	if depth > 32 {
		return nil, protocolError("cursor_argument_depth")
	}
	m, err := parse(b)
	if err != nil {
		return nil, err
	}
	if len(m) != 1 {
		return nil, protocolError("cursor_invalid_argument_value")
	}
	f := m[0]
	switch f.number {
	case 1:
		if f.wire == 0 && f.value == 0 {
			return nil, nil
		}
	case 2:
		if f.wire == 1 {
			n := math.Float64frombits(binary.LittleEndian.Uint64(f.data))
			if !math.IsInf(n, 0) && !math.IsNaN(n) {
				return n, nil
			}
		}
	case 3:
		if f.wire == 2 && utf8.Valid(f.data) {
			return string(f.data), nil
		}
	case 4:
		if f.wire == 0 && f.value <= 1 {
			return f.value == 1, nil
		}
	case 5, 6:
		if f.wire != 2 {
			break
		}
		entries, err := parse(f.data)
		if err != nil {
			return nil, err
		}
		obj := map[string]any{}
		arr := []any{}
		for _, e := range entries {
			if e.number != 1 || e.wire != 2 {
				return nil, protocolError("cursor_invalid_argument_value")
			}
			if f.number == 6 {
				v, err := decodeValue(e.data, depth+1)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			} else {
				entry, err := parse(e.data)
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(entry.get(2).data, depth+1)
				if err != nil {
					return nil, err
				}
				obj[entry.str(1)] = v
			}
		}
		if f.number == 5 {
			return obj, nil
		}
		return arr, nil
	}
	return nil, protocolError("cursor_invalid_argument_value")
}
