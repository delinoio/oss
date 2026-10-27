// SPDX-License-Identifier: Apache-2.0
// Wire field mappings adapted from opencodex and opencode-windsurf-auth.
// See LICENSE-MIT for the upstream notices.
package devin

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"math"
)

const maxFrame = 16 << 20

var errWire = errors.New("devin_malformed_wire")
var errLimit = errors.New("devin_response_too_large")

type field struct {
	num  int
	wire byte
	n    uint64
	b    []byte
}

func varint(b []byte, n uint64) []byte { return binary.AppendUvarint(b, n) }
func number(b []byte, tag int, n uint64) []byte {
	b = varint(b, uint64(tag<<3))
	return varint(b, n)
}
func blob(b []byte, tag int, v []byte) []byte {
	b = varint(b, uint64(tag<<3|2))
	b = varint(b, uint64(len(v)))
	return append(b, v...)
}
func textField(b []byte, tag int, v string) []byte { return blob(b, tag, []byte(v)) }
func double(b []byte, tag int, v float64) []byte {
	b = varint(b, uint64(tag<<3|1))
	return binary.LittleEndian.AppendUint64(b, math.Float64bits(v))
}

// walk rejects incomplete fields instead of accepting a truncated successful reply.
func walk(b []byte, visit func(field) error) error {
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 || tag>>3 == 0 || tag>>3 > (1<<29)-1 {
			return errWire
		}
		b = b[n:]
		f := field{num: int(tag >> 3), wire: byte(tag & 7)}
		switch f.wire {
		case 0:
			f.n, n = binary.Uvarint(b)
			if n <= 0 {
				return errWire
			}
			b = b[n:]
		case 1, 5:
			size := 8
			if f.wire == 5 {
				size = 4
			}
			if len(b) < size {
				return errWire
			}
			f.b, b = b[:size], b[size:]
		case 2:
			size, used := binary.Uvarint(b)
			if used <= 0 {
				return errWire
			}
			b = b[used:]
			if size > uint64(len(b)) {
				return errWire
			}
			f.b, b = b[:int(size)], b[int(size):]
		default:
			return errWire
		}
		if err := visit(f); err != nil {
			return err
		}
	}
	return nil
}

func frame(b []byte) []byte {
	out := make([]byte, 5, 5+len(b))
	binary.BigEndian.PutUint32(out[1:], uint32(len(b)))
	return append(out, b...)
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var h [5]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	if h[0] & ^byte(3) != 0 {
		return 0, nil, errWire
	}
	size := binary.BigEndian.Uint32(h[1:])
	if size > maxFrame {
		return 0, nil, errLimit
	}
	b := make([]byte, int(size))
	if _, err := io.ReadFull(r, b); err != nil {
		return 0, nil, err
	}
	if h[0]&1 != 0 {
		z, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return 0, nil, errWire
		}
		b, err = io.ReadAll(io.LimitReader(z, maxFrame+1))
		closeErr := z.Close()
		if len(b) > maxFrame {
			return 0, nil, errLimit
		}
		if err != nil || closeErr != nil {
			return 0, nil, errWire
		}
	}
	return h[0], b, nil
}
