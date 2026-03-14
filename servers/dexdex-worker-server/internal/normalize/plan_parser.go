package normalize

import (
	"encoding/json"
	"strings"

	"gopkg.in/yaml.v3"
)

// PlanStep represents a structured step in a plan.
type PlanStep struct {
	Title       string   `json:"title" yaml:"title"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
	FilePaths   []string `json:"file_paths,omitempty" yaml:"file_paths,omitempty"`
	Status      string   `json:"status,omitempty" yaml:"status,omitempty"`
}

// StructuredPlan is the parsed output of a plan YAML.
type StructuredPlan struct {
	Title      string     `json:"title" yaml:"title"`
	Steps      []PlanStep `json:"steps" yaml:"steps"`
	Complexity string     `json:"complexity,omitempty" yaml:"complexity,omitempty"`
}

// ParsePlanContent attempts to parse plan content as YAML and returns
// structured JSON. If parsing fails, returns the original content unchanged.
func ParsePlanContent(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return body
	}

	var plan StructuredPlan
	if err := yaml.Unmarshal([]byte(trimmed), &plan); err != nil {
		return body
	}

	// Validate that parsing produced meaningful structure.
	if plan.Title == "" && len(plan.Steps) == 0 {
		return body
	}

	jsonBytes, err := json.Marshal(&plan)
	if err != nil {
		return body
	}

	return string(jsonBytes)
}
