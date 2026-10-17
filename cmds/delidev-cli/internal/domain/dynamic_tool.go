// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
)

const CodexDynamicToolsV1 WorkerCapability = "codex-dynamic-tools-v1"
const DynamicToolUnavailableText = "This dynamic tool is unavailable in DeliDev."

type DynamicToolStatus string
type DynamicToolStage string
type DynamicContentKind string
type DynamicReplyDelivery string

const (
	DynamicRunning     DynamicToolStatus    = "inProgress"
	DynamicDone        DynamicToolStatus    = "completed"
	DynamicFailed      DynamicToolStatus    = "failed"
	DynamicStarted     DynamicToolStage     = "started"
	DynamicCompleted   DynamicToolStage     = "completed"
	DynamicRequested   DynamicToolStage     = "requested"
	DynamicReplied     DynamicToolStage     = "replied"
	DynamicResolved    DynamicToolStage     = "resolved"
	DynamicText        DynamicContentKind   = "inputText"
	DynamicImage       DynamicContentKind   = "inputImage"
	DynamicAudio       DynamicContentKind   = "inputAudio"
	DynamicNotSent     DynamicReplyDelivery = "not-sent"
	DynamicTransmitted DynamicReplyDelivery = "transmitted"
	DynamicUncertain   DynamicReplyDelivery = "uncertain"
)

// Arguments and media locators stay in original protected native history.
// Their commitments retain identity without creating retrieval or dispatch authority.
type DynamicToolIdentity struct {
	Tool            string  `json:"tool"`
	Namespace       *string `json:"namespace"`
	ArgumentsDigest string  `json:"arguments_digest"`
}
type DynamicToolContent struct {
	Kind            DynamicContentKind `json:"kind"`
	Text            *string            `json:"text,omitempty"`
	ReferenceDigest string             `json:"reference_digest,omitempty"`
}
type DynamicToolSnapshot struct {
	Identity   DynamicToolIdentity   `json:"identity"`
	Status     DynamicToolStatus     `json:"status"`
	Content    *[]DynamicToolContent `json:"content"`
	Success    *bool                 `json:"success"`
	DurationMS *int64                `json:"duration_ms,string"`
}

// Preserve numeric IDs as exact decimal strings; text IDs occupy another namespace.
type DynamicToolRequest struct {
	Token    ID                       `json:"token"`
	IDKind   InteractionRequestIDKind `json:"id_kind"`
	IDValue  string                   `json:"id_value"`
	Identity DynamicToolIdentity      `json:"identity"`
}
type DynamicToolReply struct {
	Token       ID                   `json:"token"`
	ClaimID     ID                   `json:"claim_id"`
	Delivery    DynamicReplyDelivery `json:"delivery"`
	Unavailable bool                 `json:"unavailable"`
}
type DynamicToolUpdate struct {
	ID       ID                   `json:"id"`
	NativeID string               `json:"native_id"`
	Stage    DynamicToolStage     `json:"stage"`
	Snapshot *DynamicToolSnapshot `json:"snapshot,omitempty"`
	Request  *DynamicToolRequest  `json:"request,omitempty"`
	Reply    *DynamicToolReply    `json:"reply,omitempty"`
	Token    ID                   `json:"token,omitempty"`
}
type DynamicTool struct {
	Snapshot *DynamicToolSnapshot `json:"snapshot,omitempty"`
	Request  *DynamicToolRequest  `json:"request,omitempty"`
	Reply    *DynamicToolReply    `json:"reply,omitempty"`
	Resolved bool                 `json:"resolved"`
}

var dynamicDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (i DynamicToolIdentity) Validate() error {
	if Text(i.Tool, "native dynamic tool", 256, true) != nil || i.Namespace != nil && Text(*i.Namespace, "native dynamic namespace", 256, false) != nil || !dynamicDigest.MatchString(i.ArgumentsDigest) {
		return invalidTool()
	}
	return nil
}
func (s DynamicToolSnapshot) Validate() error {
	if s.Identity.Validate() != nil || s.Status != "inProgress" && s.Status != "completed" && s.Status != "failed" || s.DurationMS != nil && *s.DurationMS < 0 {
		return invalidTool()
	}
	if s.Content != nil {
		if len(*s.Content) > 1024 {
			return invalidTool()
		}
		for _, p := range *s.Content {
			if p.Kind == DynamicText {
				if p.Text == nil || Text(*p.Text, "native dynamic text", MaxMessageText, false) != nil || p.ReferenceDigest != "" {
					return invalidTool()
				}
			} else if p.Kind == DynamicImage || p.Kind == DynamicAudio {
				if p.Text != nil || !dynamicDigest.MatchString(p.ReferenceDigest) {
					return invalidTool()
				}
			} else {
				return invalidTool()
			}
		}
	}
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > 512<<10 {
		return invalidTool()
	}
	return nil
}
func (r DynamicToolRequest) Validate() error {
	if r.Token.Validate() != nil || r.Identity.Validate() != nil {
		return invalidTool()
	}
	if r.IDKind == InteractionTextID {
		if Text(r.IDValue, "native dynamic request", 128, true) != nil {
			return invalidTool()
		}
	} else if r.IDKind != InteractionNumberID || !interactionDecimal.MatchString(r.IDValue) {
		return invalidTool()
	}
	if r.IDKind == InteractionNumberID {
		if _, err := strconv.ParseInt(r.IDValue, 10, 64); err != nil {
			return invalidTool()
		}
	}
	return nil
}
func (u DynamicToolUpdate) Validate() error {
	if u.ID.Validate() != nil || Text(u.NativeID, "native dynamic call", 1024, true) != nil {
		return invalidTool()
	}
	switch u.Stage {
	case DynamicStarted, DynamicCompleted:
		if u.Snapshot == nil || u.Snapshot.Validate() != nil || (u.Stage == DynamicStarted) != (u.Snapshot.Status == "inProgress") || u.Request != nil || u.Reply != nil || u.Token != "" {
			return invalidTool()
		}
	case DynamicRequested:
		if u.Request == nil || u.Request.Validate() != nil || u.Snapshot != nil || u.Reply != nil || u.Token != "" {
			return invalidTool()
		}
	case DynamicReplied:
		if u.Reply == nil || u.Reply.Token.Validate() != nil || u.Reply.ClaimID.Validate() != nil || u.Reply.Delivery != DynamicNotSent && u.Reply.Delivery != DynamicTransmitted && u.Reply.Delivery != DynamicUncertain || u.Snapshot != nil || u.Request != nil || u.Token != "" {
			return invalidTool()
		}
	case DynamicResolved:
		if u.Token.Validate() != nil || u.Snapshot != nil || u.Request != nil || u.Reply != nil {
			return invalidTool()
		}
	default:
		return invalidTool()
	}
	return nil
}

// Reduce independent facts without manufacturing native success or completion.
func ApplyDynamicTool(prior *DynamicTool, u DynamicToolUpdate) (DynamicTool, error) {
	if u.Validate() != nil {
		return DynamicTool{}, invalidTool()
	}
	rawUpdate, err := json.Marshal(u)
	if err != nil || Decode(rawUpdate, &u) != nil {
		return DynamicTool{}, invalidTool()
	}
	next := DynamicTool{}
	if prior != nil {
		raw, _ := json.Marshal(prior)
		if Decode(raw, &next) != nil {
			return next, invalidTool()
		}
	}
	switch u.Stage {
	case DynamicStarted, DynamicCompleted:
		if next.Snapshot != nil && (next.Snapshot.Status != "inProgress" || u.Stage != DynamicCompleted || !reflect.DeepEqual(next.Snapshot.Identity, u.Snapshot.Identity)) || next.Request != nil && !reflect.DeepEqual(next.Request.Identity, u.Snapshot.Identity) {
			return next, invalidTool()
		}
		next.Snapshot = u.Snapshot
	case DynamicRequested:
		if next.Request != nil || next.Snapshot != nil && (next.Snapshot.Status != "inProgress" || !reflect.DeepEqual(next.Snapshot.Identity, u.Request.Identity)) {
			return next, invalidTool()
		}
		next.Request = u.Request
	case DynamicReplied:
		if next.Request == nil || next.Reply != nil || next.Request.Token != u.Reply.Token || next.Resolved {
			return next, invalidTool()
		}
		next.Reply = u.Reply
	case DynamicResolved:
		if next.Request == nil || next.Resolved || next.Request.Token != u.Token {
			return next, invalidTool()
		}
		next.Resolved = true
	}
	return next, nil
}
func (d DynamicTool) Settled() bool {
	return d.Snapshot != nil && d.Snapshot.Status != "inProgress" && (d.Request == nil || d.Resolved && d.Reply != nil && d.Reply.Delivery == DynamicTransmitted)
}
