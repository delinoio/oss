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

var errMalformedJSONStream = errors.New("security: malformed JSON string escape in streamed text")

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
// Its retained state is bounded by the longest protected form. The JSON profile
// may hold its equivalent escaped spelling, including one incomplete escape.
// It never keeps the whole stream. It is finite literal/Base64 reflection
// protection like ProtectedJSON.
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
	// JSON strings need a second matching window: their raw spelling can use
	// different escapes for the same decoded bytes. Starts retain the original
	// byte position of each decoded byte so Push can withhold the raw spelling.
	jsonForms       []string
	jsonMax         int
	jsonTail        []byte
	jsonStarts      []int64
	jsonOffset      int64
	jsonInString    bool
	jsonEscape      jsonEscapeState
	jsonEscapeStart int64
	jsonDigits      uint8
	jsonUnit        uint16
	jsonHigh        uint16
	jsonHighStart   int64
}

type jsonEscapeState uint8

const (
	jsonEscapeNone jsonEscapeState = iota
	jsonEscapeSlash
	jsonEscapeUnicode
	jsonEscapePairSlash
	jsonEscapePairU
	jsonEscapePairUnicode
)

// NewStream returns a matcher for one independent stream. It returns nil when
// the guard has no protected forms; every method of a nil stream is a
// pass-through, so callers need no separate unconfigured branch.
func (g ProtectedJSON) NewStream(profile StreamProfile) *ProtectedStream {
	if len(g.forms) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(g.forms)*2)
	seenJSON := make(map[string]bool, len(g.forms))
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
			if !seenJSON[form] {
				seenJSON[form] = true
				stream.jsonForms = append(stream.jsonForms, form)
				stream.jsonMax = max(stream.jsonMax, len(form))
			}
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

// Settle marks the withheld suffix as published through another verified path,
// such as a complete snapshot that was itself checked as one frame. It keeps the
// matching window so a later fragment still cannot complete a protected form
// with the now published bytes, but nothing is returned or released again.
func (s *ProtectedStream) Settle() {
	if s == nil {
		return
	}
	clear(s.held)
	s.held = s.held[:0]
}

// Clone returns an independent copy for a transactional rehearsal. Mutating
// either stream never changes the other.
func (s *ProtectedStream) Clone() *ProtectedStream {
	if s == nil {
		return nil
	}
	clone := *s
	clone.tail = append([]byte(nil), s.tail...)
	clone.held = append([]byte(nil), s.held...)
	clone.jsonTail = append([]byte(nil), s.jsonTail...)
	clone.jsonStarts = append([]int64(nil), s.jsonStarts...)
	return &clone
}

// Discard clears every retained byte without publishing it. Use it on failure,
// cancellation, interruption and ownership replacement.
func (s *ProtectedStream) Discard() {
	if s == nil {
		return
	}
	clear(s.held)
	clear(s.tail)
	clear(s.jsonTail)
	clear(s.jsonStarts)
	s.held, s.tail = s.held[:0], s.tail[:0]
	s.jsonTail, s.jsonStarts = s.jsonTail[:0], s.jsonStarts[:0]
	s.jsonOffset, s.jsonInString, s.jsonEscape = 0, false, jsonEscapeNone
	s.jsonEscapeStart, s.jsonDigits, s.jsonUnit = 0, 0, 0
	s.jsonHigh, s.jsonHighStart = 0, 0
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
	if len(s.jsonForms) > 0 {
		jsonHold, err := s.observeJSON(text)
		if err != nil {
			s.Discard()
			return 0, err
		}
		hold = max(hold, jsonHold)
	}
	return hold, nil
}

// observeJSON scans only string contents. It retains at most the decoded
// proper-prefix window and an incomplete escape or surrogate pair. The
// original bytes remain solely in Push's bounded holdback and raw window.
func (s *ProtectedStream) observeJSON(text string) (int, error) {
	for i := 0; i < len(text); i++ {
		position := s.jsonOffset
		s.jsonOffset++
		if err := s.consumeJSONByte(text[i], position); err != nil {
			return 0, err
		}
	}
	start := s.jsonOffset
	for length := len(s.jsonTail); length > 0; length-- {
		suffix := s.jsonTail[len(s.jsonTail)-length:]
		for _, form := range s.jsonForms {
			if len(form) > length && bytes.Equal(suffix, []byte(form[:length])) {
				start = min(start, s.jsonStarts[len(s.jsonStarts)-length])
			}
		}
	}
	if s.jsonEscape != jsonEscapeNone {
		if s.jsonEscape == jsonEscapePairSlash || s.jsonEscape == jsonEscapePairU || s.jsonEscape == jsonEscapePairUnicode {
			start = min(start, s.jsonHighStart)
		} else {
			start = min(start, s.jsonEscapeStart)
		}
	}
	return int(s.jsonOffset - start), nil
}

func (s *ProtectedStream) consumeJSONByte(value byte, position int64) error {
	if !s.jsonInString {
		if value == '"' {
			s.jsonInString = true
			s.clearJSONWindow()
		}
		return nil
	}
	switch s.jsonEscape {
	case jsonEscapeSlash:
		s.jsonEscape = jsonEscapeNone
		switch value {
		case '"', '\\', '/':
			return s.emitJSONByte(value, s.jsonEscapeStart)
		case 'b':
			return s.emitJSONByte('\b', s.jsonEscapeStart)
		case 'f':
			return s.emitJSONByte('\f', s.jsonEscapeStart)
		case 'n':
			return s.emitJSONByte('\n', s.jsonEscapeStart)
		case 'r':
			return s.emitJSONByte('\r', s.jsonEscapeStart)
		case 't':
			return s.emitJSONByte('\t', s.jsonEscapeStart)
		case 'u':
			s.jsonEscape = jsonEscapeUnicode
			s.jsonDigits, s.jsonUnit = 0, 0
			return nil
		default:
			return errMalformedJSONStream
		}
	case jsonEscapeUnicode, jsonEscapePairUnicode:
		digit, ok := jsonHexDigit(value)
		if !ok {
			return errMalformedJSONStream
		}
		s.jsonUnit = s.jsonUnit<<4 | uint16(digit)
		s.jsonDigits++
		if s.jsonDigits < 4 {
			return nil
		}
		unit, start, paired := s.jsonUnit, s.jsonEscapeStart, s.jsonEscape == jsonEscapePairUnicode
		s.jsonEscape, s.jsonDigits, s.jsonUnit = jsonEscapeNone, 0, 0
		if paired {
			if unit >= 0xdc00 && unit <= 0xdfff {
				r := rune(0x10000 + (uint32(s.jsonHigh)-0xd800)*0x400 + uint32(unit) - 0xdc00)
				s.jsonHigh = 0
				return s.emitJSONRune(r, s.jsonHighStart)
			}
			if err := s.emitJSONRune(utf8.RuneError, s.jsonHighStart); err != nil {
				return err
			}
			s.jsonHigh = 0
		}
		if unit >= 0xd800 && unit <= 0xdbff {
			s.jsonHigh, s.jsonHighStart = unit, start
			s.jsonEscape = jsonEscapePairSlash
			return nil
		}
		if unit >= 0xdc00 && unit <= 0xdfff {
			return s.emitJSONRune(utf8.RuneError, start)
		}
		return s.emitJSONRune(rune(unit), start)
	case jsonEscapePairSlash:
		if value == '\\' {
			s.jsonEscape, s.jsonEscapeStart = jsonEscapePairU, position
			return nil
		}
		if err := s.emitJSONRune(utf8.RuneError, s.jsonHighStart); err != nil {
			return err
		}
		s.jsonHigh, s.jsonEscape = 0, jsonEscapeNone
		return s.consumeJSONByte(value, position)
	case jsonEscapePairU:
		if value == 'u' {
			s.jsonEscape, s.jsonDigits, s.jsonUnit = jsonEscapePairUnicode, 0, 0
			return nil
		}
		if err := s.emitJSONRune(utf8.RuneError, s.jsonHighStart); err != nil {
			return err
		}
		s.jsonHigh, s.jsonEscape = 0, jsonEscapeSlash
		return s.consumeJSONByte(value, position)
	}
	if value == '"' {
		s.jsonInString = false
		s.clearJSONWindow()
		return nil
	}
	if value == '\\' {
		s.jsonEscape, s.jsonEscapeStart = jsonEscapeSlash, position
		return nil
	}
	if value < 0x20 {
		return errMalformedJSONStream
	}
	return s.emitJSONByte(value, position)
}

func (s *ProtectedStream) emitJSONRune(value rune, position int64) error {
	var encoded [utf8.UTFMax]byte
	for _, b := range utf8.AppendRune(encoded[:0], value) {
		if err := s.emitJSONByte(b, position); err != nil {
			return err
		}
	}
	return nil
}

func (s *ProtectedStream) emitJSONByte(value byte, position int64) error {
	s.jsonTail = append(s.jsonTail, value)
	s.jsonStarts = append(s.jsonStarts, position)
	for _, form := range s.jsonForms {
		if len(s.jsonTail) >= len(form) && bytes.Equal(s.jsonTail[len(s.jsonTail)-len(form):], []byte(form)) {
			return ErrProtectedStreamValue
		}
	}
	if len(s.jsonTail) >= s.jsonMax {
		copy(s.jsonTail, s.jsonTail[1:])
		copy(s.jsonStarts, s.jsonStarts[1:])
		s.jsonTail = s.jsonTail[:len(s.jsonTail)-1]
		s.jsonStarts = s.jsonStarts[:len(s.jsonStarts)-1]
	}
	return nil
}

func (s *ProtectedStream) clearJSONWindow() {
	clear(s.jsonTail)
	clear(s.jsonStarts)
	s.jsonTail, s.jsonStarts = s.jsonTail[:0], s.jsonStarts[:0]
}

func jsonHexDigit(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
