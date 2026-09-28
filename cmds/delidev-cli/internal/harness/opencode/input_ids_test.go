package opencode

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestNativeInputIDsFollowPinnedTimestampCounterAndAlphabet(t *testing.T) {
	var generator inputIDGenerator
	entropy := func() *bytes.Reader { return bytes.NewReader(bytes.Repeat([]byte{0, 9, 10, 35, 36, 61, 62}, 4)) }
	message, part, err := generator.pair(1, entropy())
	if err != nil || message != "msg_00000000100109AZaz009AZaz0" || part != "prt_00000000100209AZaz009AZaz0" || !nativeID(message, "msg") || !nativeID(part, "prt") {
		t.Fatal("native identifier timestamp/counter or random alphabet changed")
	}
	message, _, err = generator.pair(1, entropy())
	if err != nil || !strings.HasPrefix(message, "msg_000000001003") {
		t.Fatal("same-millisecond original input counter was reused")
	}
	message, _, err = generator.pair(2, entropy())
	if err != nil || !strings.HasPrefix(message, "msg_000000002001") {
		t.Fatal("new native timestamp did not reset its own counter")
	}
	if a, b, err := generator.pair(3, bytes.NewReader(make([]byte, 14))); err == nil || a != "" || b != "" {
		t.Fatal("partial entropy failure exposed an incomplete input pair")
	}
}

func TestNativeInputIDAllocationSerializesConcurrentPairs(t *testing.T) {
	var generator inputIDGenerator
	var group sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]bool{}
	for range 64 {
		group.Go(func() {
			message, part, err := generator.pair(1700000000000, bytes.NewReader(make([]byte, 28)))
			mu.Lock()
			defer mu.Unlock()
			if err != nil || !nativeID(message, "msg") || !nativeID(part, "prt") || seen[message[4:]] || seen[part[4:]] {
				t.Error("concurrent native input IDs collided or changed shape")
			}
			seen[message[4:]], seen[part[4:]] = true, true
		})
	}
	group.Wait()
	if len(seen) != 128 {
		t.Fatal("native input pair allocation lost identities")
	}
}
