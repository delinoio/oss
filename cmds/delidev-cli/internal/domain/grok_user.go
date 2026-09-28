package domain

import (
	"crypto/sha256"
	"encoding/hex"
)

// GrokUserHistory describes a native user record verified after original close.
// Its event is a persisted update identity, not a live message notification.
type GrokUserHistory struct {
	Source        GrokTerminalKind `json:"source"`
	NativeEventID string           `json:"native_event_id"`
	TimestampMS   string           `json:"timestamp_ms"`
	PromptIndex   string           `json:"prompt_index"`
	Model         string           `json:"model"`
	InputDigest   string           `json:"input_digest"`
}

func GrokUserInputDigest(input string) string {
	digest := sha256.Sum256([]byte(input))
	return hex.EncodeToString(digest[:])
}

func (v GrokUserHistory) Validate(thread string) error {
	if v.Source != GrokClosedFirstText || v.PromptIndex != "0" || Text(v.Model, "original Grok model", 256, true) != nil {
		return invalidGrokContent()
	}
	if _, err := GrokEventIndex(v.NativeEventID, thread); err != nil {
		return err
	}
	if n, ok := grokCount(v.TimestampMS); !ok || n > 253402300799999 {
		return invalidGrokContent()
	}
	digest, err := hex.DecodeString(v.InputDigest)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != v.InputDigest {
		return invalidGrokContent()
	}
	return nil
}
