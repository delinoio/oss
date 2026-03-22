package session

import (
	"crypto/rand"
	"encoding/binary"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func NewULID(now time.Time) (string, error) {
	timestamp := uint64(now.UnixMilli())
	if timestamp > (1<<48)-1 {
		return "", errmsg.Error("timestamp exceeds ULID max", map[string]any{
			"timestamp_ms": timestamp,
			"max_ms":       uint64((1 << 48) - 1),
		})
	}

	buf := make([]byte, 16)
	binary.BigEndian.PutUint64(buf[:8], timestamp<<16)
	if _, err := rand.Read(buf[6:16]); err != nil {
		return "", errmsg.Error(errmsg.Runtime("read random bytes", err, map[string]any{
			"random_bytes": len(buf[6:16]),
		}), nil)
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
