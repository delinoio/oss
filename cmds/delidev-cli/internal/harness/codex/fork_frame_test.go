// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// largeHistoryTurn describes one completed fixture turn. Each output lists the
// byte length of its private fields.
type largeHistoryTurn struct {
	ID      domain.ID `json:"id"`
	Outputs [][]int   `json:"outputs"`
	// ImagesOnly makes every part an input_image; ClientID and Prompt bind the
	// original user input; ContextItemID adds an original contextCompaction item.
	ImagesOnly    bool      `json:"imagesOnly,omitempty"`
	ClientID      domain.ID `json:"clientId,omitempty"`
	Prompt        string    `json:"prompt,omitempty"`
	ContextItemID string    `json:"contextItemId,omitempty"`
}

// frameProfileFixture opens the enlarged original decoder profile. The fixture
// builds oversized private history itself because client requests stay bounded.
func frameProfileFixture(t *testing.T, turns []largeHistoryTurn) *Client {
	t.Helper()
	return framePaddedFixture(t, turns, 0)
}

// framePaddedFixture also pads the page envelope with an unused cursor of the
// given length, which adds page bytes but no turn bytes.
func framePaddedFixture(t *testing.T, turns []largeHistoryTurn, cursorPad int) *Client {
	t.Helper()
	c, _ := framePaddedBoundFixture(t, turns, cursorPad)
	return c
}

// framePaddedBoundFixture also returns the resumed thread binding so callers can
// derive exact continuation checkpoints.
func framePaddedBoundFixture(t *testing.T, turns []largeHistoryTurn, cursorPad int) (*Client, ThreadResult) {
	t.Helper()
	config := fixtureConfig(t, "thread-managed-image-enabled")
	config.Mode, config.ManagedAuthentication, config.EnableImageGeneration = ThreadProtocol, true, true
	config.ImageRoot, config.ImageMachineID = t.TempDir(), domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	c, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	if c.nativeFrameLimit() != 16<<20 {
		t.Fatal("enlarged native frame profile not active")
	}
	settings := threadSettings(t)
	settings.Provider = "openai"
	bound, err := c.ResumeThread(ctx, domain.NewID(), domain.NewID(), settings)
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, c, "large-history", map[string]any{"turns": turns, "cursorPad": cursorPad})
	return c, bound
}

func repeatedTurns(count int, fields ...int) []largeHistoryTurn {
	turns := make([]largeHistoryTurn, 0, count)
	for range count {
		turns = append(turns, largeHistoryTurn{ID: domain.NewID(), Outputs: [][]int{fields}})
	}
	return turns
}

func TestForkTurnsAcceptPrivateHistoryAboveDomainBoundUnderNativeFrameProfile(t *testing.T) {
	const field = 700 << 10
	cases := []struct {
		name  string
		turns []largeHistoryTurn
	}{
		// The aggregate page and the only turn both exceed 1 MiB.
		{"single-turn-above-1MiB", repeatedTurns(1, field, field)},
		// Only the enclosing page exceeds 1 MiB; each turn stays below it.
		{"page-above-1MiB-with-small-turns", repeatedTurns(3, field)},
		{"aggregate-just-below-4MiB", repeatedTurns(5, 800<<10)},
		// One private field alone exceeds the public 1 MiB document limit.
		{"single-field-above-1MiB", repeatedTurns(1, 1200<<10)},
	}
	for _, managed := range []bool{false, true} {
		for _, test := range cases {
			name := test.name
			if managed {
				name = "managed-" + name
			}
			t.Run(name, func(t *testing.T) {
				c := frameProfileFixture(t, test.turns)
				c.managedForkHistory = managed
				fork, err := c.forkTurnsLocked(context.Background(), c.thread)
				if err != nil || len(fork) != len(test.turns) {
					t.Fatal("eligible private history rejected under the native frame profile", err, len(fork))
				}
				total := 0
				for _, turn := range fork {
					total += len(turn)
				}
				if total <= 1<<20 || total > 4<<20 {
					t.Fatal("fixture does not exercise the bound between 1 MiB and 4 MiB", total)
				}
				// Normalization keeps the original private bytes.
				if !bytes.Contains(fork[0], []byte(strings.Repeat("a", test.turns[0].Outputs[0][0]))) {
					t.Fatal("original private bytes were not retained")
				}
				if len(test.turns[0].Outputs[0]) == 2 && !bytes.Contains(fork[0], []byte(strings.Repeat("b", test.turns[0].Outputs[0][1]))) {
					t.Fatal("original ciphertext bytes were not retained")
				}
				// A repeated read is stable, so unchanged history stays comparable.
				again, err := c.forkTurnsLocked(context.Background(), c.thread)
				if err != nil || historyDigest(again) != historyDigest(fork) {
					t.Fatal("normalized history is not stable", err)
				}
			})
		}
	}
}

func TestForkTurnsRejectPrivateHistoryAboveDomainBoundUnderLowerFrameProfile(t *testing.T) {
	const field = 700 << 10
	for _, test := range []struct {
		name  string
		turns []largeHistoryTurn
	}{
		{"single-turn", repeatedTurns(1, field, field)},
		{"page-of-small-turns", repeatedTurns(3, field)},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := frameProfileFixture(t, test.turns)
			// Force the base decoder profile while the already read frame stays large.
			c.imageObservations = false
			_, err := c.forkTurnsLocked(context.Background(), c.thread)
			if err == nil {
				t.Fatal("history above the lower frame profile was accepted")
			}
			for _, secret := range []string{"aaaaaaaaaa", "bbbbbbbbbb"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("rejection exposed private bytes")
				}
			}
		})
	}
}

func TestForkTurnsRejectAggregateAboveFourMiBUnderNativeFrameProfile(t *testing.T) {
	for _, managed := range []bool{false, true} {
		c := frameProfileFixture(t, repeatedTurns(6, 700<<10))
		c.managedForkHistory = managed
		if _, err := c.forkTurnsLocked(context.Background(), c.thread); err == nil {
			t.Fatal("aggregate history above 4 MiB was accepted", managed)
		}
	}
}

func TestForkTurnsCountCompletePageResultsTowardAggregateBudget(t *testing.T) {
	for _, managed := range []bool{false, true} {
		// Small turns with envelope padding below the budget stay eligible.
		c := framePaddedFixture(t, repeatedTurns(1, 1<<10), 3<<20)
		c.managedForkHistory = managed
		if turns, err := c.forkTurnsLocked(context.Background(), c.thread); err != nil || len(turns) != 1 {
			t.Fatal("page within the aggregate budget was rejected", managed, err)
		}
		// The same turns are rejected once the complete page result exceeds 4 MiB,
		// even though the retained turn bytes alone stay far below it.
		c = framePaddedFixture(t, repeatedTurns(1, 1<<10), 5<<20)
		c.managedForkHistory = managed
		if _, err := c.forkTurnsLocked(context.Background(), c.thread); err == nil {
			t.Fatal("page envelope bytes bypassed the aggregate budget", managed)
		}
	}
}

func TestForkTurnsLargePrivateHistoryKeepsRestrictedAndDuplicateRejections(t *testing.T) {
	const field = 700 << 10
	c := frameProfileFixture(t, repeatedTurns(1, field, field))
	c.sidechat = ReadOnlySidechatV1
	if _, err := c.forkTurnsLocked(context.Background(), c.thread); err == nil {
		t.Fatal("read-only Sidechat accepted private function output history")
	}
	c.sidechat = ""
	duplicate := repeatedTurns(2, field)
	duplicate[1].ID = duplicate[0].ID
	c = frameProfileFixture(t, duplicate)
	if _, err := c.forkTurnsLocked(context.Background(), c.thread); err == nil {
		t.Fatal("duplicate turn identity accepted")
	}
}

func TestForkTurnsPrivateLocatorsStayOutOfPublicProjection(t *testing.T) {
	locator := strings.Repeat("l", 700<<10)
	raw := `{"type":"functionCallOutput","id":"large-locator","name":"tool","output":[{"type":"input_image","image_url":"` + locator + `"},{"type":"encrypted_content","encrypted_content":"` + locator + `"}]}`
	_, output, err := decodeFunctionOutput(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	public, _ := json.Marshal(output)
	if bytes.Contains(public, []byte("lllll")) {
		t.Fatal("private locator escaped into the public projection")
	}
}

func TestForkHistoryFrameProfileIsExplicitAndManagedOnly(t *testing.T) {
	managed := Config{Mode: ThreadProtocol, ManagedAuthentication: true, ManagedForkHistory: true, FrameProfile: ForkHistoryFrameProfile}
	api := managed
	api.ManagedAuthentication, api.ManagedForkHistory, api.API = false, false, &APIConfig{}
	for _, test := range []struct {
		name   string
		config Config
		enable bool
	}{
		// Fork history enables the enlarged frame without image generation.
		{"managed-independent-fork", managed, true},
		// The managed route without the explicit option keeps the default frame.
		{"managed-fork-default", Config{Mode: ThreadProtocol, ManagedAuthentication: true, ManagedForkHistory: true, ImageRoot: "/image-root"}, false},
		{"api-fork", api, false},
		{"api-fork-default", Config{Mode: ThreadProtocol, API: &APIConfig{}, ImageRoot: "/image-root"}, false},
	} {
		if test.config.imageObservationProfile() != test.enable {
			t.Fatal("unexpected frame profile", test.name)
		}
	}
	client := Client{imageObservations: managed.imageObservationProfile()}
	if client.nativeFrameLimit() != 16<<20 {
		t.Fatal("managed independent Fork keeps the one-MiB frame")
	}
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"without-managed-fork-history", func(c *Config) { c.ManagedForkHistory = false }},
		{"with-api", func(c *Config) { c.API = &APIConfig{} }},
		{"with-sidechat", func(c *Config) { c.Sidechat = ReadOnlySidechatV1 }},
		{"unknown-profile", func(c *Config) { c.FrameProfile = "unbounded" }},
	} {
		config := managed
		test.mutate(&config)
		if _, err := Open(context.Background(), config); err == nil {
			t.Fatal("invalid frame profile request was admitted", test.name)
		}
	}
}
