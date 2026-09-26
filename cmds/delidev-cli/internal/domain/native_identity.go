package domain

import (
	"strings"

	"github.com/google/uuid"
)

// NativeIdentity is owned by the selected harness. It is not a DeliDev object
// ID and must never be regenerated or normalized into a UUID-v7 surrogate.
// Its wire representation stays the original string, including old completions.
type NativeIdentity string

type NativeIdentityKind string

const (
	NativeThreadIdentity  NativeIdentityKind = "thread"
	NativeTurnIdentity    NativeIdentityKind = "turn"
	NativeMessageIdentity NativeIdentityKind = "message"
	NativePartIdentity    NativeIdentityKind = "part"
	NativeEventIdentity   NativeIdentityKind = "event"
)

func (id NativeIdentity) Validate(harness Harness, kind NativeIdentityKind) error {
	invalid := func() error {
		return Fail(RecoveryRequired, "The original native identity does not match its selected harness profile.", "Retain the exact native session and turn identity; do not replace or reinterpret it.")
	}
	if kind != NativeThreadIdentity && kind != NativeTurnIdentity && (harness != OpenCode || kind != NativeMessageIdentity && kind != NativePartIdentity && kind != NativeEventIdentity) {
		return invalid()
	}
	switch harness {
	case Codex:
		if ID(id).Validate() != nil {
			return invalid()
		}
	case ClaudeCode:
		// DeliDev supplies the private Claude session's UUID-v7; the original
		// native init/turn envelope may instead use a canonical UUID-v4.
		if kind == NativeThreadIdentity {
			if ID(id).Validate() != nil {
				return invalid()
			}
			break
		}
		value, err := uuid.Parse(string(id))
		if err != nil || value == uuid.Nil || value.Variant() != uuid.RFC4122 || (value.Version() != 4 && value.Version() != 7) || value.String() != string(id) {
			return invalid()
		}
	case OpenCode:
		prefix := "ses_"
		if kind == NativeTurnIdentity || kind == NativeMessageIdentity {
			// OpenCode has no separate turn UUID. The original claimed input's
			// message owns this execution boundary, even across successor
			// assistant messages. Format alone cannot prove that ownership.
			prefix = "msg_"
		}
		if kind == NativePartIdentity {
			prefix = "prt_"
		}
		if kind == NativeEventIdentity {
			prefix = "evt_"
		}
		value := string(id)
		if len(value) != len(prefix)+26 || !strings.HasPrefix(value, prefix) {
			return invalid()
		}
		for index, c := range value[len(prefix):] {
			if index < 12 {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					return invalid()
				}
			} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
				return invalid()
			}
		}
	default:
		return Fail(Unsupported, "This harness native identity profile is not implemented.", "Retain the original installed-harness evidence; do not apply another harness identity format.")
	}
	return nil
}
