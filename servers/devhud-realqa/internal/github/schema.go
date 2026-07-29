package github

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maximumDefinitionBytes = 256 * 1024

type stringList []string

func (values *stringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value == "" {
			*values = nil
			return nil
		}
		for _, value := range strings.Split(node.Value, ",") {
			*values = append(*values, strings.TrimSpace(value))
		}
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if item.Kind != yaml.ScalarNode {
				return errors.New("realqa github: definition string list is invalid")
			}
			*values = append(*values, strings.TrimSpace(item.Value))
		}
	default:
		return errors.New("realqa github: definition string list is invalid")
	}
	clean, err := cleanStringValues(*values, 100)
	if err != nil {
		return err
	}
	*values = clean
	return nil
}

func ParseMarkdownTemplate(filePath, etag string, contents []byte) (MarkdownTemplate, error) {
	definition, err := definitionRef(DefinitionMarkdown, filePath, etag)
	if err != nil {
		return MarkdownTemplate{}, err
	}
	if len(contents) > maximumDefinitionBytes || !utf8.Valid(contents) {
		return MarkdownTemplate{}, errors.New("realqa github: Markdown template is invalid")
	}
	normalized := normalizeNewlines(string(contents))
	if !strings.HasPrefix(normalized, "---\n") {
		definition.Name = path.Base(filePath)
		return MarkdownTemplate{Definition: definition, Body: normalized}, nil
	}
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return MarkdownTemplate{}, errors.New("realqa github: Markdown front matter is unterminated")
	}
	end += 4
	var header struct {
		Name      string     `yaml:"name"`
		About     string     `yaml:"about"`
		Title     string     `yaml:"title"`
		Type      string     `yaml:"type"`
		Labels    stringList `yaml:"labels"`
		Assignees stringList `yaml:"assignees"`
		Projects  stringList `yaml:"projects"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(normalized[4:end]))
	decoder.KnownFields(true)
	if err = decoder.Decode(&header); err != nil {
		return MarkdownTemplate{}, errors.New("realqa github: Markdown front matter is invalid")
	}
	if strings.TrimSpace(header.Name) == "" || strings.TrimSpace(header.About) == "" {
		return MarkdownTemplate{}, errors.New("realqa github: Markdown template name and about are required")
	}
	issueType, err := cleanIssueType(header.Type)
	if err != nil {
		return MarkdownTemplate{}, errors.New("realqa github: Markdown issue type is invalid")
	}
	definition.Name = strings.TrimSpace(header.Name)
	return MarkdownTemplate{
		Definition: definition, TitlePrefix: header.Title, IssueType: issueType,
		DefaultLabels: []string(header.Labels), DefaultAssignees: []string(header.Assignees),
		Body: strings.TrimPrefix(normalized[end+5:], "\n"),
	}, nil
}

type rawForm struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Title       string         `yaml:"title"`
	Type        string         `yaml:"type"`
	Labels      stringList     `yaml:"labels"`
	Assignees   stringList     `yaml:"assignees"`
	Projects    stringList     `yaml:"projects"`
	Body        []rawFormField `yaml:"body"`
}

type rawFormField struct {
	Type        string            `yaml:"type"`
	ID          string            `yaml:"id"`
	Attributes  rawFormAttributes `yaml:"attributes"`
	Validations rawFormValidation `yaml:"validations"`
}

type rawFormAttributes struct {
	Label       string      `yaml:"label"`
	Description string      `yaml:"description"`
	Placeholder string      `yaml:"placeholder"`
	Value       string      `yaml:"value"`
	Render      string      `yaml:"render"`
	Multiple    bool        `yaml:"multiple"`
	Options     []yaml.Node `yaml:"options"`
	Default     *int        `yaml:"default"`
}

type rawFormValidation struct {
	Required bool   `yaml:"required"`
	Accept   string `yaml:"accept"`
}

func ParseIssueForm(filePath, etag string, contents []byte) (IssueForm, error) {
	definition, err := definitionRef(DefinitionForm, filePath, etag)
	if err != nil {
		return IssueForm{}, err
	}
	if len(contents) == 0 || len(contents) > maximumDefinitionBytes || !utf8.Valid(contents) {
		return IssueForm{}, errors.New("realqa github: Issue Form is invalid")
	}
	var raw rawForm
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	if err = decoder.Decode(&raw); err != nil {
		return IssueForm{}, errors.New("realqa github: Issue Form schema is invalid")
	}
	formName := strings.TrimSpace(raw.Name)
	if utf8.RuneCountInString(formName) <= 3 ||
		strings.TrimSpace(raw.Description) == "" ||
		len(raw.Body) == 0 || len(raw.Body) > 100 {
		return IssueForm{}, errors.New("realqa github: Issue Form name, description, and body are required")
	}
	if len(raw.Projects) != 0 {
		return IssueForm{}, errors.New(
			"realqa github: Issue Form project defaults are unsupported")
	}
	issueType, err := cleanIssueType(raw.Type)
	if err != nil {
		return IssueForm{}, errors.New("realqa github: Issue Form issue type is invalid")
	}
	definition.Name = formName
	result := IssueForm{
		Definition: definition, TitlePrefix: raw.Title, IssueType: issueType,
		DefaultLabels: []string(raw.Labels), DefaultAssignees: []string(raw.Assignees),
		Fields: make([]FormField, 0, len(raw.Body)),
	}
	reservedIDs := make(map[string]struct{}, len(raw.Body))
	for _, item := range raw.Body {
		if id := strings.TrimSpace(item.ID); id != "" {
			reservedIDs[id] = struct{}{}
		}
	}
	seen := make(map[string]struct{})
	seenGeneratedLabels := make(map[string]struct{})
	hasSubmittedField := false
	for index, item := range raw.Body {
		if item.Type == "upload" {
			if err := validateSkippedUpload(item); err != nil {
				return IssueForm{}, err
			}
			continue
		}
		field, parseErr := parseFormField(item)
		if parseErr != nil {
			return IssueForm{}, parseErr
		}
		if field.Kind != FormFieldMarkdown {
			hasSubmittedField = true
			if field.ID == "" {
				if _, exists := seenGeneratedLabels[field.Label]; exists {
					return IssueForm{}, errors.New(
						"realqa github: Issue Form fields without IDs must have unique labels")
				}
				seenGeneratedLabels[field.Label] = struct{}{}
				field.ID = generatedFormFieldID(index, reservedIDs)
				reservedIDs[field.ID] = struct{}{}
			}
			if _, exists := seen[field.ID]; exists {
				return IssueForm{}, errors.New("realqa github: Issue Form field IDs must be unique")
			}
			seen[field.ID] = struct{}{}
		}
		result.Fields = append(result.Fields, field)
	}
	if !hasSubmittedField {
		return IssueForm{}, errors.New(
			"realqa github: Issue Form must contain a supported submitted field")
	}
	return result, nil
}

func parseFormField(raw rawFormField) (FormField, error) {
	field := FormField{
		ID: strings.TrimSpace(raw.ID), Label: strings.TrimSpace(raw.Attributes.Label),
		Description: strings.TrimSpace(raw.Attributes.Description),
		Placeholder: raw.Attributes.Placeholder, Required: raw.Validations.Required,
		Multiple: raw.Attributes.Multiple, Render: strings.TrimSpace(raw.Attributes.Render),
	}
	switch raw.Type {
	case string(FormFieldMarkdown):
		if field.ID != "" || strings.TrimSpace(raw.Attributes.Value) == "" {
			return FormField{}, errors.New("realqa github: Issue Form markdown is invalid")
		}
		field.Kind, field.Markdown = FormFieldMarkdown, normalizeNewlines(raw.Attributes.Value)
		return field, nil
	case string(FormFieldInput):
		field.Kind = FormFieldInput
	case string(FormFieldTextarea):
		field.Kind = FormFieldTextarea
	case string(FormFieldDropdown):
		field.Kind = FormFieldDropdown
	case string(FormFieldCheckboxes):
		field.Kind = FormFieldCheckboxes
	default:
		return FormField{}, errors.New("realqa github: Issue Form field type is unsupported")
	}
	if (field.ID != "" && !issueFormIDPattern.MatchString(field.ID)) || field.Label == "" {
		return FormField{}, errors.New("realqa github: Issue Form field ID or label is invalid")
	}
	if (field.Kind == FormFieldInput || field.Kind == FormFieldTextarea) &&
		strings.Contains(strings.ToLower(field.Label), "password") {
		return FormField{}, errors.New(
			"realqa github: Issue Form field label contains a forbidden term")
	}
	if raw.Validations.Accept != "" {
		return FormField{}, errors.New(
			"realqa github: Issue Form upload validation is not allowed for this field")
	}
	if field.Kind != FormFieldDropdown && field.Kind != FormFieldCheckboxes &&
		len(raw.Attributes.Options) != 0 {
		return FormField{}, errors.New("realqa github: Issue Form options are not allowed for this field")
	}
	if field.Multiple && field.Kind != FormFieldDropdown {
		return FormField{}, errors.New(
			"realqa github: Issue Form multiple selection is invalid")
	}
	if field.Render != "" {
		if field.Kind != FormFieldTextarea || !safeRenderLanguage(field.Render) {
			return FormField{}, errors.New(
				"realqa github: Issue Form render language is invalid")
		}
	}
	if raw.Attributes.Value != "" {
		if field.Kind != FormFieldInput && field.Kind != FormFieldTextarea {
			return FormField{}, errors.New(
				"realqa github: Issue Form default value is not allowed for this field")
		}
		field.DefaultValue = normalizeNewlines(raw.Attributes.Value)
		if strings.Contains(field.DefaultValue, "\x00") ||
			(field.Kind == FormFieldInput && strings.Contains(field.DefaultValue, "\n")) {
			return FormField{}, errors.New(
				"realqa github: Issue Form default value is invalid")
		}
	}
	if field.Kind == FormFieldDropdown || field.Kind == FormFieldCheckboxes {
		if len(raw.Attributes.Options) == 0 || len(raw.Attributes.Options) > 100 {
			return FormField{}, errors.New("realqa github: Issue Form options are required")
		}
		seenOptions := make(map[string]struct{}, len(raw.Attributes.Options))
		for _, option := range raw.Attributes.Options {
			parsed, err := parseFormOption(option, field.Kind)
			if err != nil {
				return FormField{}, err
			}
			if _, exists := seenOptions[parsed.Label]; exists {
				if field.Kind == FormFieldDropdown {
					return FormField{}, errors.New(
						"realqa github: Issue Form dropdown options must be unique")
				}
				return FormField{}, errors.New(
					"realqa github: Issue Form checkbox options must be unique")
			}
			seenOptions[parsed.Label] = struct{}{}
			field.Options = append(field.Options, parsed)
		}
	}
	if raw.Attributes.Default != nil {
		return FormField{}, errors.New(
			"realqa github: Issue Form dropdown default is unsupported")
	}
	return field, nil
}

func validateSkippedUpload(raw rawFormField) error {
	id := strings.TrimSpace(raw.ID)
	if (id != "" && !issueFormIDPattern.MatchString(id)) ||
		strings.TrimSpace(raw.Attributes.Label) == "" ||
		raw.Validations.Required ||
		raw.Attributes.Placeholder != "" ||
		raw.Attributes.Value != "" ||
		raw.Attributes.Render != "" ||
		raw.Attributes.Multiple ||
		len(raw.Attributes.Options) != 0 ||
		raw.Attributes.Default != nil ||
		strings.ContainsAny(raw.Validations.Accept, "\x00\r\n") {
		return errors.New("realqa github: Issue Form upload field is unsupported")
	}
	return nil
}

func generatedFormFieldID(index int, reserved map[string]struct{}) string {
	base := fmt.Sprintf("realqa-field-%d", index+1)
	candidate := base
	for suffix := 2; ; suffix++ {
		if _, exists := reserved[candidate]; !exists {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, suffix)
	}
}

func parseFormOption(node yaml.Node, kind FormFieldKind) (FormOption, error) {
	if node.Kind == yaml.ScalarNode && kind == FormFieldDropdown {
		if strings.TrimSpace(node.Value) == "" {
			return FormOption{}, errors.New("realqa github: Issue Form option is empty")
		}
		return FormOption{Label: strings.TrimSpace(node.Value)}, nil
	}
	if node.Kind != yaml.MappingNode || kind != FormFieldCheckboxes {
		return FormOption{}, errors.New("realqa github: Issue Form option is invalid")
	}
	var raw struct {
		Label    string `yaml:"label"`
		Required bool   `yaml:"required"`
	}
	if err := node.Decode(&raw); err != nil || strings.TrimSpace(raw.Label) == "" {
		return FormOption{}, errors.New("realqa github: Issue Form checkbox option is invalid")
	}
	return FormOption{Label: strings.TrimSpace(raw.Label), Required: raw.Required}, nil
}

func RenderIssueForm(form IssueForm, answers []FormAnswer) (string, error) {
	byID := make(map[string][]string, len(answers))
	for _, answer := range answers {
		if _, exists := byID[answer.FieldID]; exists {
			return "", errors.New("realqa github: duplicate Issue Form answer")
		}
		byID[answer.FieldID] = answer.Values
	}
	var sections []string
	for _, field := range form.Fields {
		if field.Kind == FormFieldMarkdown {
			continue
		}
		values, exists := byID[field.ID]
		if !exists {
			values = nil
		}
		clean, err := validateFormAnswer(field, values)
		if err != nil {
			return "", fmt.Errorf("realqa github: field %s: %w", field.ID, err)
		}
		delete(byID, field.ID)
		content := "_No response_"
		if len(clean) > 0 {
			if field.Kind == FormFieldCheckboxes {
				lines := make([]string, 0, len(clean))
				for _, value := range clean {
					lines = append(lines, "- [x] "+value)
				}
				content = strings.Join(lines, "\n")
			} else {
				content = strings.Join(clean, ", ")
				if field.Kind == FormFieldTextarea {
					content = strings.Join(clean, "\n")
					if field.Render != "" {
						fence := codeFence(content)
						closingSeparator := "\n"
						if strings.HasSuffix(content, "\n") {
							closingSeparator = ""
						}
						content = fence + field.Render + "\n" + content +
							closingSeparator + fence
					}
				}
			}
		}
		sections = append(sections, "### "+field.Label+"\n\n"+content)
	}
	if len(byID) != 0 {
		return "", errors.New("realqa github: answer references an unknown Issue Form field")
	}
	return strings.Join(sections, "\n\n"), nil
}

func codeFence(content string) string {
	longest, current := 2, 0
	for _, character := range content {
		if character == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	return strings.Repeat("`", longest+1)
}

func validateFormAnswer(field FormField, values []string) ([]string, error) {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeNewlines(value)
		if strings.TrimSpace(value) == "" {
			continue
		}
		if field.Kind != FormFieldTextarea {
			value = strings.TrimSpace(value)
		}
		if field.Kind == FormFieldInput && strings.Contains(value, "\n") {
			return nil, errors.New("input responses must be single-line")
		}
		clean = append(clean, value)
	}
	if (field.Kind == FormFieldInput || field.Kind == FormFieldTextarea) && len(clean) > 1 {
		return nil, errors.New("multiple values are not allowed")
	}
	if field.Required && len(clean) == 0 {
		return nil, errors.New("a response is required")
	}
	if field.Kind == FormFieldDropdown || field.Kind == FormFieldCheckboxes {
		allowed := make(map[string]FormOption, len(field.Options))
		for _, option := range field.Options {
			allowed[option.Label] = option
		}
		selected := make(map[string]struct{}, len(clean))
		for _, value := range clean {
			if _, ok := allowed[value]; !ok {
				return nil, errors.New("selected option is invalid")
			}
			if _, duplicate := selected[value]; duplicate {
				return nil, errors.New("selected option is duplicated")
			}
			selected[value] = struct{}{}
		}
		if field.Kind == FormFieldDropdown && !field.Multiple && len(clean) > 1 {
			return nil, errors.New("multiple dropdown values are not allowed")
		}
		for _, option := range field.Options {
			if option.Required {
				if _, ok := selected[option.Label]; !ok {
					return nil, errors.New("a required checkbox is not selected")
				}
			}
		}
	}
	return clean, nil
}

func safeRenderLanguage(value string) bool {
	if value == "" || len(value) > 50 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			!strings.ContainsRune("_+.-#", character) {
			return false
		}
	}
	return true
}

func cleanIssueType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > 255 || !utf8.ValidString(value) ||
		strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("realqa github: issue type is invalid")
	}
	return value, nil
}

func definitionRef(kind DefinitionKind, filePath, etag string) (DefinitionRef, error) {
	if kind != DefinitionMarkdown && kind != DefinitionForm {
		return DefinitionRef{}, errors.New("realqa github: definition kind is invalid")
	}
	cleanPath := path.Clean(filePath)
	if !strings.HasPrefix(cleanPath, ".github/ISSUE_TEMPLATE/") ||
		cleanPath != filePath || strings.TrimSpace(etag) == "" ||
		len(etag) > 255 || len(filePath) > 1024 {
		return DefinitionRef{}, errors.New("realqa github: definition reference is invalid")
	}
	return DefinitionRef{
		Kind: kind, ID: filePath, Path: filePath, ETag: etag,
	}, nil
}

func cleanStringValues(values []string, maximum int) ([]string, error) {
	if len(values) > maximum {
		return nil, errors.New("realqa github: too many values")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len([]byte(value)) > 255 || !utf8.ValidString(value) ||
			strings.ContainsAny(value, "\x00\r\n") {
			return nil, errors.New("realqa github: definition value is invalid")
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}
