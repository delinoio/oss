package domain

import "time"

// Catalog evidence never changes account readiness, model selection, or native
// harness compatibility. A failed refresh retains prior successful evidence.
type CatalogObservation struct {
	RequestID         ID               `json:"request_id"`
	ConnectionID      ID               `json:"connection_id"`
	ObservedAt        time.Time        `json:"observed_at"`
	LastSuccessAt     *time.Time       `json:"last_success_at,omitempty"`
	State             ObservationState `json:"state"`
	Received          uint32           `json:"received"`
	Added             uint32           `json:"added"`
	Updated           uint32           `json:"updated"`
	RetryAfterSeconds *uint32          `json:"retry_after_seconds,omitempty"`
	Problem           *Error           `json:"problem,omitempty"`
}

type ModelDiscovery struct {
	FirstSeenAt time.Time `json:"first_seen_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ProviderPresetID string

const (
	PresetVercel     ProviderPresetID = "vercel-ai-gateway"
	PresetOpenRouter ProviderPresetID = "openrouter"
	PresetOpenAI     ProviderPresetID = "openai"
	PresetAnthropic  ProviderPresetID = "anthropic"
	PresetXAI        ProviderPresetID = "xai"
	PresetDeepSeek   ProviderPresetID = "deepseek"
	PresetOllama     ProviderPresetID = "ollama"
	PresetLMStudio   ProviderPresetID = "lm-studio"
	PresetVLLM       ProviderPresetID = "vllm"
)

type ProviderPreset struct {
	ID            ProviderPresetID `json:"id"`
	Provider      Provider         `json:"provider"`
	Documentation string           `json:"documentation"`
	KeyGuidance   string           `json:"key_guidance"`
	Compatibility string           `json:"compatibility"`
}
