package codex

import (
	"crypto/sha256"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type ModelVerification string

const TrustedAccessForCyber ModelVerification = "trustedAccessForCyber"

type ModelRerouteReason string

const HighRiskCyberActivity ModelRerouteReason = "highRiskCyberActivity"

// These descriptors are private native evidence. Neither generic serialization
// nor an observation grants model eligibility, input or safety-override authority.
type SafetyBuffering struct {
	Model           string   `json:"-"`
	UseCases        []string `json:"-"`
	Reasons         []string `json:"-"`
	ShowBufferingUI bool     `json:"-"`
	FasterModel     *string  `json:"-"`
}
type ModelReroute struct {
	FromModel string             `json:"-"`
	ToModel   string             `json:"-"`
	Reason    ModelRerouteReason `json:"-"`
}
type SafetyObservation struct {
	ThreadID      domain.ID           `json:"-"`
	TurnID        domain.ID           `json:"-"`
	Verifications []ModelVerification `json:"-"`
	Buffering     *SafetyBuffering    `json:"-"`
	Moderation    json.RawMessage     `json:"-"`
	Reroute       *ModelReroute       `json:"-"`
}
type retainedSafety struct {
	digest      [32]byte
	size        int
	observation SafetyObservation
}

const maxSafetyMetadata = 64 << 10
const maxRetainedSafety = 1 << 20

func copySafety(value SafetyObservation) *SafetyObservation {
	value.Verifications = slices.Clone(value.Verifications)
	value.Moderation = slices.Clone(value.Moderation)
	if value.Buffering != nil {
		b := *value.Buffering
		b.UseCases = slices.Clone(b.UseCases)
		b.Reasons = slices.Clone(b.Reasons)
		b.FasterModel = copyString(b.FasterModel)
		value.Buffering = &b
	}
	if value.Reroute != nil {
		r := *value.Reroute
		value.Reroute = &r
	}
	return &value
}
func safetyStrings(values []string) bool {
	if values == nil || len(values) > 64 {
		return false
	}
	for _, value := range values {
		if domain.Text(value, "native safety descriptor", 1024, true) != nil {
			return false
		}
	}
	return true
}
func (c *Client) observeSafetyLocked(native nativewire.Event) (Event, error) {
	observation := SafetyObservation{}
	var kind MetadataKind
	switch native.Method {
	case "model/verification":
		var p struct {
			ThreadID      domain.ID           `json:"threadId"`
			TurnID        domain.ID           `json:"turnId"`
			Verifications []ModelVerification `json:"verifications"`
		}
		if domain.Decode(native.Params, &p) != nil || p.Verifications == nil || len(p.Verifications) > 64 {
			return Event{}, incompatible()
		}
		for _, v := range p.Verifications {
			if v != TrustedAccessForCyber {
				return Event{}, incompatible()
			}
		}
		observation.ThreadID, observation.TurnID, observation.Verifications = p.ThreadID, p.TurnID, p.Verifications
		kind = ModelVerificationObserved
		if len(p.Verifications) == 0 {
			kind = ModelVerificationAbsent
		}
	case "model/safetyBuffering/updated":
		var p struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
			Model    string    `json:"model"`
			UseCases []string  `json:"useCases"`
			Reasons  []string  `json:"reasons"`
			Show     *bool     `json:"showBufferingUi"`
			Faster   *string   `json:"fasterModel"`
		}
		if domain.Decode(native.Params, &p) != nil || domain.Text(p.Model, "native model", 1024, true) != nil || !safetyStrings(p.UseCases) || !safetyStrings(p.Reasons) || p.Show == nil || p.Faster != nil && domain.Text(*p.Faster, "native model", 1024, true) != nil {
			return Event{}, incompatible()
		}
		observation.ThreadID, observation.TurnID = p.ThreadID, p.TurnID
		observation.Buffering = &SafetyBuffering{Model: p.Model, UseCases: p.UseCases, Reasons: p.Reasons, ShowBufferingUI: *p.Show, FasterModel: p.Faster}
		kind = SafetyBufferingObserved
	case "turn/moderationMetadata":
		var p struct {
			ThreadID domain.ID       `json:"threadId"`
			TurnID   domain.ID       `json:"turnId"`
			Metadata json.RawMessage `json:"metadata"`
		}
		if domain.Decode(native.Params, &p) != nil || len(p.Metadata) == 0 || len(p.Metadata) > maxSafetyMetadata || !json.Valid(p.Metadata) {
			return Event{}, incompatible()
		}
		observation.ThreadID, observation.TurnID, observation.Moderation = p.ThreadID, p.TurnID, p.Metadata
		kind = ModerationMetadataObserved
	case "model/rerouted":
		var p struct {
			ThreadID domain.ID          `json:"threadId"`
			TurnID   domain.ID          `json:"turnId"`
			From     string             `json:"fromModel"`
			To       string             `json:"toModel"`
			Reason   ModelRerouteReason `json:"reason"`
		}
		if domain.Decode(native.Params, &p) != nil || domain.Text(p.From, "native model", 1024, true) != nil || domain.Text(p.To, "native model", 1024, true) != nil || p.From == p.To || p.Reason != HighRiskCyberActivity {
			return Event{}, incompatible()
		}
		observation.ThreadID, observation.TurnID = p.ThreadID, p.TurnID
		observation.Reroute = &ModelReroute{FromModel: p.From, ToModel: p.To, Reason: p.Reason}
		kind = ModelRerouteObserved
	default:
		return Event{}, incompatible()
	}
	if observation.ThreadID.Validate() != nil || observation.TurnID.Validate() != nil {
		return Event{}, incompatible()
	}
	if observation.ThreadID != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[observation.TurnID]
	if !known {
		return Event{}, incompatible()
	}
	if observation.Reroute != nil && observation.Reroute.FromModel != c.execution.settings.Model {
		return Event{}, incompatible()
	}
	if observation.Buffering != nil {
		model := c.execution.settings.Model
		if retained, ok := c.execution.safety[observation.TurnID][ModelRerouteObserved]; ok {
			model = retained.observation.Reroute.ToModel
		}
		if observation.Buffering.Model != model {
			return Event{}, incompatible()
		}
	}
	digest := sha256.Sum256(native.Params)
	prior, exists := c.execution.safety[observation.TurnID][kind]
	if turn.Turn.Status.terminal() && (!exists || prior.digest != digest) {
		return Event{}, incompatible()
	}
	if exists && prior.digest == digest {
		event := c.metadata(kind)
		event.TurnID, event.Late, event.Safety = observation.TurnID, turn.Turn.Status.terminal(), copySafety(prior.observation)
		return event, nil
	}
	if kind == ModelRerouteObserved && exists {
		return Event{}, incompatible()
	}
	size := len(native.Params)
	if size > maxSafetyMetadata || c.execution.safetyBytes-prior.size+size > maxRetainedSafety {
		return Event{}, incompatible()
	}
	if c.execution.safety == nil {
		c.execution.safety = map[domain.ID]map[MetadataKind]retainedSafety{}
	}
	if c.execution.safety[observation.TurnID] == nil {
		c.execution.safety[observation.TurnID] = map[MetadataKind]retainedSafety{}
	}
	c.execution.safety[observation.TurnID][kind] = retainedSafety{digest: digest, size: size, observation: *copySafety(observation)}
	c.execution.safetyBytes += size - prior.size
	if kind == ModelRerouteObserved {
		// Keep native safety enforced and actual-model evidence privately. Original
		// reconciliation is required; selected settings and usage are never rewritten.
		c.execution.paused = true
		if c.problem == nil {
			c.problem = turnUncertain()
		}
	}
	event := c.metadata(kind)
	event.TurnID, event.Late, event.Safety = observation.TurnID, turn.Turn.Status.terminal(), copySafety(observation)
	return event, nil
}
