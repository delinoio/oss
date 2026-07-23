// Package uuidv7 generates RFC 9562 UUID version 7 identifiers with injectable
// time and randomness.
package uuidv7

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Clock makes UUID timestamps deterministic in tests.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Generator is safe for concurrent use and preserves lexical order for IDs
// generated within the same millisecond by incrementing the 12-bit rand_a
// field. Clock rollback is pinned to the most recent timestamp.
type Generator struct {
	clock  Clock
	random io.Reader

	mu       sync.Mutex
	lastMS   uint64
	sequence uint16
}

// NewGenerator constructs a UUID v7 generator.
func NewGenerator(clock Clock, random io.Reader) *Generator {
	if clock == nil {
		clock = systemClock{}
	}
	if random == nil {
		random = rand.Reader
	}
	return &Generator{clock: clock, random: random}
}

var defaultGenerator = NewGenerator(nil, nil)

// New generates a UUID v7 with the process default generator.
func New() (uuid.UUID, error) {
	return defaultGenerator.New()
}

// MustNew generates a UUID v7 or panics if the secure random source fails.
func MustNew() uuid.UUID {
	id, err := New()
	if err != nil {
		panic(err)
	}
	return id
}

// New generates one UUID v7.
func (g *Generator) New() (uuid.UUID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	signedMS := g.clock.Now().UnixMilli()
	if signedMS < 0 || signedMS > (1<<48)-1 {
		return uuid.Nil, errors.New("uuidv7: timestamp is outside the 48-bit Unix millisecond range")
	}
	nowMS := uint64(signedMS)
	if nowMS < g.lastMS {
		nowMS = g.lastMS
	}

	var randomBytes [10]byte
	if _, err := io.ReadFull(g.random, randomBytes[:]); err != nil {
		return uuid.Nil, errors.New("uuidv7: random source failed")
	}

	if nowMS == g.lastMS {
		g.sequence = (g.sequence + 1) & 0x0fff
		if g.sequence == 0 {
			// Sequence exhaustion is exceptionally unlikely in normal server
			// workloads. Advancing the encoded millisecond preserves uniqueness
			// and ordering without blocking on an injectable clock.
			nowMS++
		}
	} else {
		g.sequence = binary.BigEndian.Uint16(randomBytes[:2]) & 0x0fff
	}
	g.lastMS = nowMS

	var id uuid.UUID
	id[0] = byte(nowMS >> 40)
	id[1] = byte(nowMS >> 32)
	id[2] = byte(nowMS >> 24)
	id[3] = byte(nowMS >> 16)
	id[4] = byte(nowMS >> 8)
	id[5] = byte(nowMS)
	id[6] = 0x70 | byte(g.sequence>>8)
	id[7] = byte(g.sequence)
	copy(id[8:], randomBytes[2:])
	id[8] = (id[8] & 0x3f) | 0x80
	return id, nil
}

// Time returns the millisecond timestamp encoded in an RFC 9562 UUID v7.
func Time(id uuid.UUID) (time.Time, error) {
	if id.Version() != 7 {
		return time.Time{}, errors.New("uuidv7: UUID is not version 7")
	}
	milliseconds := uint64(id[0])<<40 |
		uint64(id[1])<<32 |
		uint64(id[2])<<24 |
		uint64(id[3])<<16 |
		uint64(id[4])<<8 |
		uint64(id[5])
	return time.UnixMilli(int64(milliseconds)).UTC(), nil
}
