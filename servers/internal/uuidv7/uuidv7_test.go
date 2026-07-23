package uuidv7

import (
	"bytes"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

func TestUUIDV7PropertiesAndOrdering(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 23, 12, 34, 56, 789_000_000, time.UTC)
	generator := NewGenerator(fixedClock{now: now}, bytes.NewReader(make([]byte, 40)))

	first, err := generator.New()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generator.New()
	if err != nil {
		t.Fatal(err)
	}
	if first.Version() != 7 || first.Variant() != uuid.RFC4122 {
		t.Fatalf("UUID version/variant = %d/%v", first.Version(), first.Variant())
	}
	decoded, err := Time(first)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(now.Truncate(time.Millisecond)) {
		t.Fatalf("decoded time = %s, want %s", decoded, now.Truncate(time.Millisecond))
	}
	if first.String() >= second.String() {
		t.Fatalf("UUIDs are not ordered: %s >= %s", first, second)
	}
}

func TestUUIDV7RejectsRandomFailureAndWrongVersion(t *testing.T) {
	t.Parallel()
	generator := NewGenerator(fixedClock{now: time.Now()}, bytes.NewReader(nil))
	if _, err := generator.New(); err == nil {
		t.Fatal("New() succeeded with exhausted random reader")
	}
	if _, err := Time(uuid.New()); err == nil {
		t.Fatal("Time() accepted a non-v7 UUID")
	}
	beforeEpoch := NewGenerator(fixedClock{now: time.UnixMilli(-1)}, bytes.NewReader(make([]byte, 10)))
	if _, err := beforeEpoch.New(); err == nil {
		t.Fatal("New() accepted a pre-Unix timestamp")
	}
}
