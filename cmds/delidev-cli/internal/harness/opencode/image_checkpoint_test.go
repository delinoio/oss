package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// imageIdentity stands in for the attachment-aware assignment digest the
// Worker passes with an image-bearing input. It never equals the text digest.
func imageIdentity(text string) [sha256.Size]byte {
	return sha256.Sum256([]byte("attachment-aware:" + text))
}

// newImageHistoryFixture completes a fake native image turn: the original text
// part, the synthetic Read part and the verified image file part, then a
// finished assistant message and the settled idle state.
func newImageHistoryFixture(t *testing.T) (*historyFixture, [sha256.Size]byte) {
	t.Helper()
	first, raw := fixtureInputImage(t, 0)
	input := imageFixtureInput(t, first)
	input.identity = imageIdentity("describe")
	observed := newImageObserver(t, input)
	observed.message(observed.user)
	observed.part(observed.input)
	observed.part(syntheticFor(input, (1)))
	observed.part(nativeFilePart(t, input, 0, raw))
	observed.status(NativeStatusBusy)
	observed.message(observed.a)
	observed.finish(FinishStop)
	observed.status(NativeStatusIdle)
	observed.observe(SessionIdleEvent, map[string]any{"sessionID": fixtureSessionID})
	// The native server stores parts ordered by identifier, not arrival.
	f := &historyFixture{t: t, o: observed.o, original: storedMessage(t, input, observed.input, nativeFilePart(t, input, 0, raw), syntheticFor(input, (1)))}
	o := f.o
	creation, stored := o.creation, o.input
	session := &sessionAPI{creation: &creation, input: &stored, observer: o, events: &eventStream{ctx: context.Background()}, gate: make(chan struct{}, 1), cwd: o.cwd, runtimeRoot: o.root, password: "private-http-secret", origin: "http://127.0.0.1:1", alive: func() error { return nil }}
	session.client = &http.Client{Transport: f}
	f.api = &OwnedAPI{session: session, reading: make(chan struct{}, 1)}
	return f, input.identity
}

func TestImageTurnCheckpointRetainsTheAttachmentAwareInputIdentity(t *testing.T) {
	f := mustImageHistory(t)
	prepareHistoryCleanup(f.f, func(context.Context) error { return nil })
	ctx := context.Background()
	if _, err := f.f.api.CloseCompleted(ctx); err != nil {
		t.Fatalf("image turn history was not verified: %v", err)
	}
	home := attachCheckpointFixture(t, f.f.api)
	raw, ref, err := f.f.api.RetainCheckpoint(ctx)
	if err != nil {
		t.Fatalf("image turn checkpoint retention failed: %v", err)
	}
	textDigest := sha256.Sum256([]byte("describe"))
	if ref.InputSHA256 != hex.EncodeToString(f.identity[:]) || ref.InputSHA256 == hex.EncodeToString(textDigest[:]) {
		t.Fatal("checkpoint reference did not retain the attachment-aware input identity")
	}
	if err := InspectCheckpoint(ctx, home, raw, ref); err != nil {
		t.Fatalf("retained image turn checkpoint failed inspection: %v", err)
	}
	other := ref
	other.InputSHA256 = hex.EncodeToString(textDigest[:])
	if InspectCheckpoint(ctx, home, raw, other) == nil {
		t.Fatal("text-only identity was accepted for an image-bearing checkpoint")
	}
	if strings.Contains(string(raw), "worker/image") {
		t.Fatal("private image path entered the checkpoint")
	}
}

type imageHistory struct {
	f        *historyFixture
	identity [sha256.Size]byte
}

func mustImageHistory(t *testing.T) imageHistory {
	t.Helper()
	f, identity := newImageHistoryFixture(t)
	return imageHistory{f: f, identity: identity}
}

func TestTextOnlyCheckpointKeepsTheHistoricalInputIdentity(t *testing.T) {
	api, _ := completedCheckpointFixture(t)
	_, ref, err := api.RetainCheckpoint(context.Background())
	textDigest := sha256.Sum256([]byte("private input"))
	if err != nil || ref.InputSHA256 != hex.EncodeToString(textDigest[:]) {
		t.Fatalf("text-only checkpoint identity changed: %v", err)
	}
}

func TestInputIdentityBindsAttachmentsAndKeepsTextOnlyDigest(t *testing.T) {
	image, _ := fixtureInputImage(t, 0)
	text := sha256.Sum256([]byte("describe"))
	for name, test := range map[string]struct {
		images   []InputImage
		identity [sha256.Size]byte
		valid    bool
	}{
		"text-only historical digest":        {nil, text, true},
		"text-only with attachment identity": {nil, imageIdentity("describe"), false},
		"text-only zero":                     {nil, [sha256.Size]byte{}, false},
		"image-bearing attachment identity":  {[]InputImage{image}, imageIdentity("describe"), true},
		"image-bearing text digest":          {[]InputImage{image}, text, false},
		"image-bearing zero":                 {[]InputImage{image}, [sha256.Size]byte{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if validInputIdentity("describe", test.images, test.identity) != test.valid {
				t.Fatalf("validity = %v, want %v", !test.valid, test.valid)
			}
		})
	}
	input := imageFixtureInput(t, image)
	if input.inputIdentity() != input.identity {
		t.Fatal("image-bearing input did not use its attachment identity")
	}
	plain := sessionInput{digest: text}
	if plain.inputIdentity() != text {
		t.Fatal("text-only input did not keep its text digest")
	}
}

func TestStartInputRejectsAMismatchedIdentityBeforeClaimingTheListener(t *testing.T) {
	image, _ := fixtureInputImage(t, 0)
	for name, test := range map[string]struct {
		images   []InputImage
		identity [sha256.Size]byte
	}{
		"text-only attachment identity": {nil, imageIdentity("describe")},
		"image-bearing text digest":     {[]InputImage{image}, sha256.Sum256([]byte("describe"))},
		"image-bearing zero":            {[]InputImage{image}, [sha256.Size]byte{}},
	} {
		t.Run(name, func(t *testing.T) {
			api := &OwnedAPI{session: &sessionAPI{creation: &sessionCreation{request: domain.NewID(), settings: fixtureSettings()}, gate: make(chan struct{}, 1), alive: func() error { return nil }}, reading: make(chan struct{}, 1)}
			if _, err := api.StartInput(context.Background(), domain.NewID(), "describe", test.images, test.identity); err == nil || api.session.eventAttempt || api.session.events != nil || api.session.input != nil {
				t.Fatal("mismatched input identity reached native work")
			}
		})
	}
}
