// SPDX-License-Identifier: Apache-2.0
package security

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// ErrProtectedStreamValue reports that an ordered text stream completed a
// protected form. The matcher is cleared before it returns this error, and the
// completing fragment is never released.
var ErrProtectedStreamValue = errors.New("security: protected value completed across streamed text")

// StreamProfile selects how protected forms appear inside a streamed text.
type StreamProfile string

const (
	// StreamPlainText matches the protected literal and Base64 forms verbatim.
	StreamPlainText StreamProfile = "plain-text"
	// StreamJSONText also matches the JSON string escaped spelling of every
	// form, for streams that carry partial JSON documents.
	StreamJSONText StreamProfile = "json-text"
)

// ProtectedStream detects protected forms across the ordered fragments of one
// logical text stream (one content block, one message part or one response).
// Its retained state is bounded by the longest protected form: it keeps only the
// trailing bytes that are still a proper prefix of some form, never the whole
// stream. It is finite literal/Base64 reflection protection like ProtectedJSON.
//
// A ProtectedStream is not safe for concurrent use. Create one per independent
// stream; independent streams never share matching state.
type ProtectedStream struct {
	forms   []string
	maxHold int
	// tail holds the latest bytes observed, already published or not, so a
	// form spanning several fragments is still found.
	tail []byte
	// held is the unpublished suffix withheld by Push.
	held []byte
}

// NewStream returns a matcher for one independent stream. It returns nil when
// the guard has no protected forms; every method of a nil stream is a
// pass-through, so callers need no separate unconfigured branch.
func (g ProtectedJSON) NewStream(profile StreamProfile) *ProtectedStream {
	if len(g.forms) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(g.forms)*2)
	stream := &ProtectedStream{}
	add := func(form string) {
		if form != "" && !seen[form] {
			seen[form] = true
			stream.forms = append(stream.forms, form)
			if len(form)-1 > stream.maxHold {
				stream.maxHold = len(form) - 1
			}
		}
	}
	for _, form := range g.forms {
		add(form)
		if profile == StreamJSONText {
			var encoded bytes.Buffer
			encoder := json.NewEncoder(&encoded)
			encoder.SetEscapeHTML(false)
			if encoder.Encode(form) == nil {
				text := strings.TrimSuffix(encoded.String(), "\n")
				if len(text) >= 2 {
					add(text[1 : len(text)-1])
				}
			}
		}
	}
	return stream
}

// Prime records text that was already published before matching started, such
// as the initial text of a content block. It is never withheld or released.
func (s *ProtectedStream) Prime(text string) error {
	if s == nil {
		return nil
	}
	_, err := s.observe(text)
	return err
}

// Observe records a fragment without owning its publication and returns how many
// trailing bytes of the whole stream still form a proper prefix of a protected
// form. Callers that publish whole items, such as native chunks, may publish
// every item that ends before the final unresolved bytes. A completed protected
// form returns ErrProtectedStreamValue after the matcher is cleared.
func (s *ProtectedStream) Observe(text string) (int, error) {
	if s == nil {
		return 0, nil
	}
	return s.observe(text)
}

// Push records a fragment and returns the part that is proven unable to start
// or complete a protected form. The unresolved trailing bytes stay private and
// are folded into the next returned fragment, so the concatenation of returned
// fragments is always a prefix of the original stream in order.
func (s *ProtectedStream) Push(text string) (string, error) {
	if s == nil {
		return text, nil
	}
	hold, err := s.observe(text)
	if err != nil {
		return "", err
	}
	pending := make([]byte, 0, len(s.held)+len(text))
	pending = append(append(pending, s.held...), text...)
	cut := len(pending) - min(hold, len(pending))
	for cut > 0 && cut < len(pending) && !utf8.RuneStart(pending[cut]) {
		cut--
	}
	release := string(pending[:cut])
	clear(s.held)
	s.held = append(s.held[:0], pending[cut:]...)
	clear(pending)
	return release, nil
}

// Held returns the number of withheld bytes.
func (s *ProtectedStream) Held() int {
	if s == nil {
		return 0
	}
	return len(s.held)
}

// Flush returns and clears the withheld suffix. Call it only when the stream
// ended cleanly and its complete text is already proven free of protected forms,
// so nothing can extend the withheld bytes any more.
func (s *ProtectedStream) Flush() string {
	if s == nil {
		return ""
	}
	release := string(s.held)
	s.Discard()
	return release
}

// Discard clears every retained byte without publishing it. Use it on failure,
// cancellation, interruption and ownership replacement.
func (s *ProtectedStream) Discard() {
	if s == nil {
		return
	}
	clear(s.held)
	clear(s.tail)
	s.held, s.tail = s.held[:0], s.tail[:0]
}

func (s *ProtectedStream) observe(text string) (int, error) {
	window := make([]byte, 0, len(s.tail)+len(text))
	window = append(append(window, s.tail...), text...)
	defer clear(window)
	for _, form := range s.forms {
		if bytes.Contains(window, []byte(form)) {
			s.Discard()
			return 0, ErrProtectedStreamValue
		}
	}
	hold := 0
	for k := min(len(window), s.maxHold); k > 0 && hold == 0; k-- {
		suffix := string(window[len(window)-k:])
		for _, form := range s.forms {
			if len(form) > k && strings.HasPrefix(form, suffix) {
				hold = k
				break
			}
		}
	}
	keep := min(len(window), s.maxHold)
	s.tail = append(s.tail[:0], window[len(window)-keep:]...)
	return hold, nil
}
