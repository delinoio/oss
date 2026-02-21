package session

import (
	"strings"
	"testing"
	"time"
)

func TestNewULIDFormat(t *testing.T) {
	id, err := NewULID(time.Now().UTC())
	if err != nil {
		t.Fatalf("NewULID returned error: %v", err)
	}
	if len(id) != 26 {
		t.Fatalf("unexpected id length: got=%d want=26", len(id))
	}
	if strings.ContainsAny(id, "ilo") {
		t.Fatalf("id contains invalid lowercase letters: %s", id)
	}
	for _, ch := range id {
		if !(ch >= '0' && ch <= '9') && !(ch >= 'A' && ch <= 'Z') {
			t.Fatalf("id contains non-crockford character: %c", ch)
		}
	}
}
