// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
)

// MCPElicitationKind is the closed set of form field shapes the product can answer.
type MCPElicitationKind string

const (
	MCPElicitationString  MCPElicitationKind = "string"
	MCPElicitationNumber  MCPElicitationKind = "number"
	MCPElicitationInteger MCPElicitationKind = "integer"
	MCPElicitationBoolean MCPElicitationKind = "boolean"
	MCPElicitationEnum    MCPElicitationKind = "enum"
	MCPElicitationMulti   MCPElicitationKind = "multi"
)

// MCPElicitationActionQuestion is the question that carries accept, decline or cancel.
const MCPElicitationActionQuestion = "action"

const (
	maxMCPElicitationFields  = 64
	maxMCPElicitationOptions = 128
)

// MCPElicitationField is one answerable field of the original native form with
// every constraint needed to validate an answer before the response is accepted.
type MCPElicitationField struct {
	Key        string             `json:"key"`
	QuestionID string             `json:"question_id"`
	Kind       MCPElicitationKind `json:"kind"`
	Required   bool               `json:"required,omitempty"`
	Options    []string           `json:"options,omitempty"`
	Minimum    *float64           `json:"minimum,omitempty"`
	Maximum    *float64           `json:"maximum,omitempty"`
	MinLength  *uint64            `json:"min_length,omitempty"`
	MaxLength  *uint64            `json:"max_length,omitempty"`
	MinItems   *uint64            `json:"min_items,omitempty"`
	MaxItems   *uint64            `json:"max_items,omitempty"`
}

// MCPElicitationSchema is the typed, non-secret form retained with a managed MCP
// elicitation so the server validates a response before the Worker claims it.
type MCPElicitationSchema struct {
	Fields []MCPElicitationField `json:"fields"`
}

func invalidMCPElicitation() error {
	return Fail(InvalidArgument, "The answer does not match the original MCP request.", "Answer accept, decline or cancel and provide each offered field only when accepting.")
}

func (s *MCPElicitationSchema) Validate() error {
	if s == nil || len(s.Fields) > maxMCPElicitationFields {
		return invalidInteraction()
	}
	seen := map[string]bool{MCPElicitationActionQuestion: true}
	for _, field := range s.Fields {
		if Text(field.Key, "MCP field", 100, true) != nil || field.QuestionID != "field:"+field.Key || len(field.QuestionID) > 128 || seen[field.QuestionID] || len(field.Options) > maxMCPElicitationOptions {
			return invalidInteraction()
		}
		seen[field.QuestionID] = true
		switch field.Kind {
		case MCPElicitationString, MCPElicitationNumber, MCPElicitationInteger, MCPElicitationBoolean, MCPElicitationEnum, MCPElicitationMulti:
		default:
			return invalidInteraction()
		}
		for _, option := range field.Options {
			if Text(option, "MCP option", 4096, true) != nil {
				return invalidInteraction()
			}
		}
	}
	return nil
}

// Response maps the owner's answers to the native action and content. Decline
// and cancel carry no content, so a refused write has no value that could be
// misread as consent. It is the single validation used by the server before
// acceptance and by the Worker before the native reply.
func (s *MCPElicitationSchema) Response(answers map[string][]string) (string, map[string]any, error) {
	if s == nil || len(answers) != len(s.Fields)+1 {
		return "", nil, invalidMCPElicitation()
	}
	action := answers[MCPElicitationActionQuestion]
	if len(action) != 1 || !slices.Contains([]string{"accept", "decline", "cancel"}, action[0]) {
		return "", nil, invalidMCPElicitation()
	}
	if action[0] != "accept" {
		for _, field := range s.Fields {
			if len(answers[field.QuestionID]) != 0 {
				return "", nil, invalidMCPElicitation()
			}
		}
		return action[0], nil, nil
	}
	content := map[string]any{}
	for _, field := range s.Fields {
		values, present := answers[field.QuestionID]
		if !present || values == nil {
			return "", nil, invalidMCPElicitation()
		}
		if len(values) == 0 {
			if field.Required {
				return "", nil, invalidMCPElicitation()
			}
			continue
		}
		value, ok := field.NativeValue(values)
		if !ok {
			return "", nil, invalidMCPElicitation()
		}
		content[field.Key] = value
	}
	return "accept", content, nil
}

// ValidateAnswers reports whether answers form a legal response to the form.
func (s *MCPElicitationSchema) ValidateAnswers(answers map[string][]string) error {
	_, _, err := s.Response(answers)
	return err
}

// NativeValue validates one field's answers against its constraints and returns
// the typed native value.
func (field MCPElicitationField) NativeValue(values []string) (any, bool) {
	if field.Kind != MCPElicitationMulti && len(values) != 1 {
		return nil, false
	}
	switch field.Kind {
	case MCPElicitationString:
		length := uint64(len([]rune(values[0])))
		if field.MinLength != nil && length < *field.MinLength || field.MaxLength != nil && length > *field.MaxLength {
			return nil, false
		}
		return values[0], true
	case MCPElicitationBoolean:
		switch values[0] {
		case "true":
			return true, true
		case "false":
			return false, true
		}
		return nil, false
	case MCPElicitationEnum:
		return values[0], slices.Contains(field.Options, values[0])
	case MCPElicitationInteger:
		// Parse as a signed 64-bit integer: the native response is an int64 and the
		// MCP integer schema has no 53-bit cutoff. Bounds compare exactly.
		text := strings.TrimSpace(values[0])
		number, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			// An integral float spelling such as "12.0" or "1e3" stays accepted when exact.
			float, floatErr := strconv.ParseFloat(text, 64)
			if floatErr != nil || math.IsNaN(float) || math.IsInf(float, 0) || float != math.Trunc(float) || math.Abs(float) > 1<<53 {
				return nil, false
			}
			number = int64(float)
		}
		exact := new(big.Float).SetInt64(number)
		if field.Minimum != nil && exact.Cmp(big.NewFloat(*field.Minimum)) < 0 || field.Maximum != nil && exact.Cmp(big.NewFloat(*field.Maximum)) > 0 {
			return nil, false
		}
		return number, true
	case MCPElicitationNumber:
		number, err := strconv.ParseFloat(strings.TrimSpace(values[0]), 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || field.Minimum != nil && number < *field.Minimum || field.Maximum != nil && number > *field.Maximum {
			return nil, false
		}
		return number, true
	case MCPElicitationMulti:
		count := uint64(len(values))
		if field.MinItems != nil && count < *field.MinItems || field.MaxItems != nil && count > *field.MaxItems {
			return nil, false
		}
		seen := map[string]bool{}
		for _, value := range values {
			if !slices.Contains(field.Options, value) || seen[value] {
				return nil, false
			}
			seen[value] = true
		}
		return slices.Clone(values), true
	}
	return nil, false
}

// matches reports whether the generic questions are exactly the action question
// followed by one question per field, so the schema describes what was persisted.
func (s *MCPElicitationSchema) matches(questions *QuestionRequest) bool {
	if s == nil || questions == nil || len(questions.Questions) != len(s.Fields)+1 || questions.Questions[0].ID != MCPElicitationActionQuestion {
		return false
	}
	for i, field := range s.Fields {
		if questions.Questions[i+1].ID != field.QuestionID {
			return false
		}
	}
	return true
}
