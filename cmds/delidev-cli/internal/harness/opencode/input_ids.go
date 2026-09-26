package opencode

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Follow the pinned official schema identifier generator's low six timestamp/
// counter bytes and fourteen base-62 random characters. These are native API
// client-provided message/part identities, not credentials or product UUIDs.
// Session IDs remain exclusively native creation results. Timestamp wrapping
// and clock movement are native semantics, not a replay cursor or sort promise.
type inputIDGenerator struct {
	mu      sync.Mutex
	last    int64
	counter uint64
}

var originalInputIDs inputIDGenerator

func (g *inputIDGenerator) pair(milliseconds int64, entropy io.Reader) (string, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if milliseconds < 0 || entropy == nil {
		return "", "", sessionInvalid()
	}
	if milliseconds != g.last {
		g.last, g.counter = milliseconds, 0
	}
	create := func(prefix string) (string, error) {
		g.counter++
		value := (uint64(milliseconds)*0x1000 + g.counter) & 0xffffffffffff
		var suffix [14]byte
		if _, err := io.ReadFull(entropy, suffix[:]); err != nil {
			return "", unavailable()
		}
		const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
		for i, b := range suffix {
			// This follows native byte % 62 spelling exactly. These random
			// identifiers never serve as authentication tokens.
			suffix[i] = alphabet[int(b)%len(alphabet)]
		}
		return fmt.Sprintf("%s_%012x%s", prefix, value, suffix), nil
	}
	message, err := create("msg")
	if err != nil {
		return "", "", err
	}
	part, err := create("prt")
	if err != nil {
		return "", "", err
	}
	return message, part, nil
}

// submitText allocates fresh native input IDs, then delegates to the original
// once-claimed transport. A response error retains its actual receipt/claim;
// this method never recreates a session or substitutes IDs for an earlier send.
func (s *sessionAPI) submitText(ctx context.Context, request domain.ID, text string) (InputReceipt, error) {
	message, part, err := freshInputIDPair()
	if err != nil {
		return InputReceipt{}, err
	}
	return s.submit(ctx, request, message, part, text)
}

func freshInputIDPair() (string, string, error) {
	return originalInputIDs.pair(time.Now().UnixMilli(), rand.Reader)
}
