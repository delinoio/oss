package domain

import "github.com/google/uuid"

// NativeIdentity is owned by the selected harness. It is not a DeliDev object
// ID and must never be regenerated or normalized into a UUID-v7 surrogate.
// Its wire representation stays the original string, including old completions.
type NativeIdentity string

type NativeIdentityKind string

const (
	NativeThreadIdentity NativeIdentityKind = "thread"
	NativeTurnIdentity   NativeIdentityKind = "turn"
)

func (id NativeIdentity) Validate(harness Harness, kind NativeIdentityKind) error {
	invalid := func() error {
		return Fail(RecoveryRequired, "The original native identity does not match its selected harness profile.", "Retain the exact native session and turn identity; do not replace or reinterpret it.")
	}
	if kind != NativeThreadIdentity && kind != NativeTurnIdentity {
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
	default:
		return Fail(Unsupported, "This harness native identity profile is not implemented.", "Retain the original installed-harness evidence; do not apply another harness identity format.")
	}
	return nil
}
