package providers

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

func advisoryMetadata(item map[string]json.RawMessage, key []byte, model *Model) error {
	if raw := item["architecture"]; len(raw) > 0 && string(raw) != "null" {
		architecture, err := object(raw)
		if err != nil {
			return err
		}
		for name, target := range map[string]*[]string{"input_modalities": &model.InputModalities, "output_modalities": &model.OutputModalities} {
			if raw := architecture[name]; len(raw) > 0 && string(raw) != "null" {
				if json.Unmarshal(raw, target) != nil || len(*target) > 16 {
					return errors.New("invalid modalities")
				}
				for _, value := range *target {
					if len(value) == 0 || len(value) > 32 || containsKey(value, key) || strings.IndexFunc(value, func(r rune) bool { return !(r >= 'a' && r <= 'z') && r != '-' }) >= 0 {
						return errors.New("invalid modality")
					}
				}
			}
		}
	}
	if raw := item["supported_parameters"]; len(raw) > 0 && string(raw) != "null" {
		var values []string
		if json.Unmarshal(raw, &values) != nil || len(values) > 256 {
			return errors.New("invalid supported parameters")
		}
		tools, reasoning := slices.Contains(values, "tools"), slices.Contains(values, "reasoning")
		model.Tools = &tools
		model.Reasoning = &reasoning
	}
	return nil
}

// OpenRouter's current paginated endpoint supplies a count. Older compatible
// responses may omit it, in which case a short page terminates the bounded walk.
// Never follow provider-supplied pagination URLs or reuse their query parameters.
func routerPage(raw []byte, offset, count int) (bool, error) {
	if count > 500 {
		return false, errors.New("oversized page")
	}
	obj, err := object(raw)
	if err != nil {
		return false, err
	}
	if value := obj["total_count"]; len(value) > 0 {
		var total uint32
		if json.Unmarshal(value, &total) != nil || string(value) == "null" || int(total) < offset+count {
			return false, errors.New("invalid catalog count")
		}
		more := int(total) > offset+count
		if more && count != 500 {
			return false, errors.New("incomplete catalog page")
		}
		return more, nil
	}
	return count == 500, nil
}
