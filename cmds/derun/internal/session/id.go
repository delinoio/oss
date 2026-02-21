package session

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func NewULID(now time.Time) (string, error) {
	timestamp := uint64(now.UnixMilli())
	if timestamp > (1<<48)-1 {
		return "", fmt.Errorf("timestamp exceeds ULID max: %d", timestamp)
	}

	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[:8], timestamp<<16)
	if _, err := rand.Read(buf[6:16]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	out := make([]byte, 26)
	var value uint32
	var bits uint
	outPos := 0

	for i := 0; i < len(buf); i++ {
		value = (value << 8) | uint32(buf[i])
		bits += 8
		for bits >= 5 {
			bits -= 5
			idx := (value >> bits) & 31
			out[outPos] = crockford[idx]
			outPos++
		}
	}
	if bits > 0 {
		idx := (value << (5 - bits)) & 31
		out[outPos] = crockford[idx]
		outPos++
	}
	for outPos < 26 {
		out[outPos] = crockford[0]
		outPos++
	}

	return string(out[:26]), nil
}
