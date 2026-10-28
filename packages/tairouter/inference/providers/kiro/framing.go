// SPDX-License-Identifier: Apache-2.0
// AWS event-stream framing follows the pinned opencodex wire contract.
package kiro

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"unicode/utf8"
)

const maxFrameBytes = 16 << 20
const maxHeaderBytes = 128 << 10

type frame struct {
	headers map[string]string
	payload []byte
}

func readFrame(r io.Reader) (frame, error) {
	var prelude [12]byte
	if _, err := io.ReadFull(r, prelude[:]); err != nil {
		return frame{}, err
	}
	total, headers := binary.BigEndian.Uint32(prelude[:4]), binary.BigEndian.Uint32(prelude[4:8])
	if total < 16 || total > maxFrameBytes || headers > maxHeaderBytes || headers > total-16 {
		return frame{}, failure(502, "kiro_invalid_frame_length")
	}
	if crc32.ChecksumIEEE(prelude[:8]) != binary.BigEndian.Uint32(prelude[8:]) {
		return frame{}, failure(502, "kiro_prelude_crc_mismatch")
	}
	remaining := make([]byte, int(total)-12)
	if _, err := io.ReadFull(r, remaining); err != nil {
		return frame{}, err
	}
	checksum := crc32.Update(crc32.ChecksumIEEE(prelude[:]), crc32.IEEETable, remaining[:len(remaining)-4])
	if checksum != binary.BigEndian.Uint32(remaining[len(remaining)-4:]) {
		return frame{}, failure(502, "kiro_message_crc_mismatch")
	}
	parsed, err := frameHeaders(remaining[:int(headers)])
	if err != nil {
		return frame{}, err
	}
	return frame{parsed, remaining[int(headers) : len(remaining)-4]}, nil
}

func frameHeaders(b []byte) (map[string]string, error) {
	out := map[string]string{}
	bad := func() (map[string]string, error) { return nil, failure(502, "kiro_invalid_frame_header") }
	for len(b) > 0 {
		n := int(b[0])
		b = b[1:]
		if n == 0 || len(b) < n+1 || !utf8.Valid(b[:n]) {
			return bad()
		}
		name := string(b[:n])
		kind := b[n]
		b = b[n+1:]
		if _, exists := out[name]; exists {
			return bad()
		}
		value := ""
		size := 0
		switch kind {
		case 0, 1:
		case 2:
			size = 1
		case 3:
			size = 2
		case 4:
			size = 4
		case 5, 8:
			size = 8
		case 9:
			size = 16
		case 6, 7:
			if len(b) < 2 {
				return bad()
			}
			size = int(binary.BigEndian.Uint16(b[:2]))
			b = b[2:]
		default:
			return bad()
		}
		if len(b) < size {
			return bad()
		}
		if kind == 7 {
			if !utf8.Valid(b[:size]) {
				return bad()
			}
			value = string(b[:size])
		}
		if len(name) > 0 && name[0] == ':' && kind != 7 {
			return bad()
		}
		out[name] = value
		b = b[size:]
	}
	return out, nil
}
