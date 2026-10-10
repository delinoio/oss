// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
)

// The closed public profile is an observation, never a tool dispatch contract.
// Arguments and media references remain in protected original native history.
type DynamicToolStage string
type DynamicToolStatus string
type DynamicReplyDelivery string
type DynamicArgumentType string
type DynamicContentType string

const (
	DynamicToolStarted   DynamicToolStage     = "started"
	DynamicToolCompleted DynamicToolStage     = "completed"
	DynamicToolRequested DynamicToolStage     = "requested"
	DynamicToolReplied   DynamicToolStage     = "reply-delivery"
	DynamicToolResolved  DynamicToolStage     = "request-resolved"
	DynamicInProgress    DynamicToolStatus    = "inProgress"
	DynamicCompleted     DynamicToolStatus    = "completed"
	DynamicFailed        DynamicToolStatus    = "failed"
	DynamicNotSent       DynamicReplyDelivery = "not-sent"
	DynamicSendStarted   DynamicReplyDelivery = "send-started"
	DynamicTransmitted   DynamicReplyDelivery = "transmitted"
	DynamicUncertain     DynamicReplyDelivery = "uncertain"
	DynamicNull          DynamicArgumentType  = "null"
	DynamicObject        DynamicArgumentType  = "object"
	DynamicArray         DynamicArgumentType  = "array"
	DynamicString        DynamicArgumentType  = "string"
	DynamicNumber        DynamicArgumentType  = "number"
	DynamicBoolean       DynamicArgumentType  = "boolean"
	DynamicText          DynamicContentType   = "inputText"
	DynamicImage         DynamicContentType   = "inputImage"
	DynamicAudio         DynamicContentType   = "inputAudio"
)
const DynamicUnavailableText = "This dynamic tool is unavailable in DeliDev."

type DynamicArguments struct {
	Present bool                `json:"present"`
	Type    DynamicArgumentType `json:"type"`
	Digest  string              `json:"digest"`
}
type DynamicContent struct {
	Type             DynamicContentType `json:"type"`
	Text             *string            `json:"text,omitempty"`
	ReferenceKind    string             `json:"reference_kind,omitempty"`
	ReferencePresent *bool              `json:"reference_present,omitempty"`
	ReferenceDigest  string             `json:"reference_digest,omitempty"`
}

// Numeric request identity is canonical decimal text in the public profile.
// Its kind remains number; only protected original bytes authorize a reply.
type CodexDynamicRequestID struct {
	Kind  InteractionRequestIDKind `json:"kind"`
	Value string                   `json:"value"`
}

func (id CodexDynamicRequestID) Key() (string, error) {
	switch id.Kind {
	case InteractionTextID:
		if Text(id.Value, "native dynamic request identity", 128, true) == nil {
			return "s:" + id.Value, nil
		}
	case InteractionNumberID:
		if len(id.Value) > 0 && len(id.Value) <= 20 {
			value, err := strconv.ParseInt(id.Value, 10, 64)
			if err == nil && strconv.FormatInt(value, 10) == id.Value {
				return "n:" + id.Value, nil
			}
		}
	}
	return "", InvalidDynamicTool()
}

type CodexDynamicTool struct {
	Version      uint32                 `json:"version"`
	ID           ID                     `json:"id"`
	Stage        DynamicToolStage       `json:"stage"`
	CallID       string                 `json:"call_id"`
	Namespace    *string                `json:"namespace"`
	Tool         string                 `json:"tool"`
	Arguments    DynamicArguments       `json:"arguments"`
	Status       *DynamicToolStatus     `json:"status"`
	ContentItems []DynamicContent       `json:"content_items"`
	Success      *bool                  `json:"success"`
	DurationMS   *int64                 `json:"duration_ms"`
	ArrivalID    ID                     `json:"arrival_id,omitempty"`
	RequestID    *CodexDynamicRequestID `json:"request_id,omitempty"`
	ResponseID   ID                     `json:"response_id,omitempty"`
	Delivery     DynamicReplyDelivery   `json:"delivery,omitempty"`
	// NegativeOutcome retains the fixed native response success bit (false).
	NegativeOutcome *bool `json:"negative_outcome,omitempty"`
	RequestResolved *bool `json:"request_resolved,omitempty"`
}

func DynamicDigestValid(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
}
func InvalidDynamicTool() *Error {
	return Fail(Unsupported, "The dynamic tool observation is incompatible.", "Retain the original bounded native request and history without dispatch or replay.")
}
func (d CodexDynamicTool) Validate() error {
	if d.Version != 1 || d.ID.Validate() != nil || Text(d.CallID, "native dynamic call identity", 1024, true) != nil || Text(d.Tool, "native dynamic tool", 256, true) != nil || d.Namespace != nil && Text(*d.Namespace, "native dynamic namespace", 256, false) != nil || !d.Arguments.Present || !slices.Contains([]DynamicArgumentType{DynamicNull, DynamicObject, DynamicArray, DynamicString, DynamicNumber, DynamicBoolean}, d.Arguments.Type) || !DynamicDigestValid(d.Arguments.Digest) {
		return InvalidDynamicTool()
	}
	if len(d.ContentItems) > 128 {
		return InvalidDynamicTool()
	}
	size := 0
	for _, item := range d.ContentItems {
		switch item.Type {
		case DynamicText:
			if item.Text == nil || Text(*item.Text, "dynamic output text", MaxMessageText, false) != nil || item.ReferenceKind != "" || item.ReferencePresent != nil || item.ReferenceDigest != "" {
				return InvalidDynamicTool()
			}
			size += len(*item.Text)
		case DynamicImage, DynamicAudio:
			kind := "image_url"
			if item.Type == DynamicAudio {
				kind = "audio_url"
			}
			if item.Text != nil || item.ReferenceKind != kind || item.ReferencePresent == nil || !DynamicDigestValid(item.ReferenceDigest) {
				return InvalidDynamicTool()
			}
		default:
			return InvalidDynamicTool()
		}
	}
	if size > MaxMessageText || d.DurationMS != nil && *d.DurationMS < 0 {
		return InvalidDynamicTool()
	}
	if d.Stage == DynamicToolStarted || d.Stage == DynamicToolCompleted {
		if d.Status == nil || !slices.Contains([]DynamicToolStatus{DynamicInProgress, DynamicCompleted, DynamicFailed}, *d.Status) || d.Stage == DynamicToolStarted && *d.Status != DynamicInProgress || d.Stage == DynamicToolCompleted && *d.Status == DynamicInProgress || d.ArrivalID != "" || d.RequestID != nil || d.ResponseID != "" || d.Delivery != "" || d.NegativeOutcome != nil || d.RequestResolved != nil {
			return InvalidDynamicTool()
		}
	} else {
		if !slices.Contains([]DynamicToolStage{DynamicToolRequested, DynamicToolReplied, DynamicToolResolved}, d.Stage) || d.Status != nil || d.ContentItems != nil || d.Success != nil || d.DurationMS != nil || d.ArrivalID.Validate() != nil || d.RequestID == nil || d.RequestID.Kind == InteractionDecimalID || d.RequestResolved == nil {
			return InvalidDynamicTool()
		}
		if _, err := d.RequestID.Key(); err != nil {
			return InvalidDynamicTool()
		}
		if !slices.Contains([]DynamicReplyDelivery{DynamicNotSent, DynamicSendStarted, DynamicTransmitted, DynamicUncertain}, d.Delivery) {
			return InvalidDynamicTool()
		}
		if d.Stage == DynamicToolResolved && (d.Delivery == DynamicNotSent && (d.ResponseID != "" || d.NegativeOutcome != nil) || d.Delivery != DynamicNotSent && (d.ResponseID.Validate() != nil || d.NegativeOutcome == nil || *d.NegativeOutcome)) {
			return InvalidDynamicTool()
		}
		if d.Stage == DynamicToolRequested && (d.Delivery != DynamicNotSent || d.ResponseID != "" || d.NegativeOutcome != nil || *d.RequestResolved) || d.Stage == DynamicToolReplied && (d.ResponseID.Validate() != nil || d.NegativeOutcome == nil || *d.NegativeOutcome || *d.RequestResolved || d.Delivery == DynamicNotSent) || d.Stage == DynamicToolResolved && !*d.RequestResolved {
			return InvalidDynamicTool()
		}
	}
	return nil
}

// A nullable observation field is required; omission is not native null.
func (d *CodexDynamicTool) UnmarshalJSON(raw []byte) error {
	type plain CodexDynamicTool
	var value plain
	var fields map[string]json.RawMessage
	if Decode(raw, &fields) != nil || Decode(raw, &value) != nil {
		return InvalidDynamicTool()
	}
	for _, key := range []string{"version", "id", "stage", "call_id", "namespace", "tool", "arguments", "status", "content_items", "success", "duration_ms"} {
		if len(fields[key]) == 0 {
			return InvalidDynamicTool()
		}
	}
	*d = CodexDynamicTool(value)
	return d.Validate()
}
