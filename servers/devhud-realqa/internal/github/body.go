package github

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

func SubmissionMarker(submissionID uuid.UUID) (string, error) {
	if submissionID == uuid.Nil || submissionID.Version() != 7 {
		return "", errors.New("realqa github: submission ID must be UUID v7")
	}
	return "realqa:submission:" + submissionID.String(), nil
}

func ComposeBody(input IssueInput) (string, error) {
	marker, err := SubmissionMarker(input.SubmissionID)
	if err != nil {
		return "", err
	}
	if !utf8.ValidString(input.RepositoryResponse) ||
		strings.Contains(input.RepositoryResponse, "\x00") {
		return "", errors.New("realqa github: repository response is invalid")
	}
	sections := make([]string, 0, 4)
	if response := strings.TrimSpace(normalizeNewlines(input.RepositoryResponse)); response != "" {
		sections = append(sections, response)
	}
	capture, err := renderCapture(input.Capture)
	if err != nil {
		return "", err
	}
	sections = append(sections, capture)
	images, err := renderImages(input.Images)
	if err != nil {
		return "", err
	}
	if images != "" {
		sections = append(sections, images)
	}
	sections = append(sections, "<!-- "+marker+" -->")
	body := strings.Join(sections, "\n\n") + "\n"
	if len([]byte(body)) > BodyByteLimit {
		return "", fmt.Errorf("realqa github: issue body exceeds 60,000 UTF-8 bytes")
	}
	return body, nil
}

func renderCapture(capture CaptureMetadata) (string, error) {
	lines := []string{"## RealQA capture"}
	for _, field := range capture.Environment {
		key := strings.TrimSpace(field.Key)
		value := strings.TrimSpace(normalizeNewlines(field.Value))
		if key == "" || value == "" || strings.ContainsAny(key, "\x00\r\n") ||
			strings.Contains(value, "\x00") {
			return "", errors.New("realqa github: capture environment field is invalid")
		}
		lines = append(lines, "- **"+escapeInline(key)+":** "+escapeInline(value))
	}
	if capture.SanitizedURL != "" {
		parsed, err := url.Parse(capture.SanitizedURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.Host == "" || parsed.User != nil || strings.Contains(capture.SanitizedURL, "\x00") {
			return "", errors.New("realqa github: capture URL is invalid")
		}
		lines = append(lines, "- **URL:** <"+capture.SanitizedURL+">")
	}
	if capture.DOM != nil {
		dom := capture.DOM
		if dom.ViewportWidth < 0 || dom.ViewportHeight < 0 {
			return "", errors.New("realqa github: DOM viewport is invalid")
		}
		values := []CaptureField{
			{Key: "DOM selector", Value: dom.CSSSelector},
			{Key: "DOM tag", Value: dom.Tag},
			{Key: "DOM role", Value: dom.Role},
			{Key: "DOM accessible name", Value: dom.AccessibleName},
		}
		for _, field := range values {
			if field.Value != "" {
				if strings.ContainsAny(field.Value, "\x00\r\n") {
					return "", errors.New("realqa github: DOM metadata is invalid")
				}
				lines = append(lines, "- **"+field.Key+":** `"+
					strings.ReplaceAll(field.Value, "`", "\\`")+"`")
			}
		}
		if dom.ViewportWidth > 0 && dom.ViewportHeight > 0 {
			lines = append(lines, "- **DOM viewport:** "+
				strconv.FormatInt(int64(dom.ViewportWidth), 10)+"×"+
				strconv.FormatInt(int64(dom.ViewportHeight), 10))
		}
	}
	if len(lines) == 1 {
		lines = append(lines, "_No capture metadata included._")
	}
	return strings.Join(lines, "\n"), nil
}

func renderImages(images []InlineImage) (string, error) {
	lines := make([]string, 0, len(images))
	for index, image := range images {
		parsed, err := url.Parse(image.URL)
		if err != nil || parsed.Scheme != "https" ||
			parsed.Host != "assets.realqa.deli.dev" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" ||
			parsed.Path == "" || parsed.Path == "/" {
			return "", errors.New("realqa github: inline image URL is invalid")
		}
		alt := strings.TrimSpace(image.AltText)
		if alt == "" {
			alt = "RealQA capture " + strconv.Itoa(index+1)
		}
		if strings.ContainsAny(alt, "\x00\r\n") {
			return "", errors.New("realqa github: inline image alt text is invalid")
		}
		alt = strings.NewReplacer("[", "\\[", "]", "\\]").Replace(alt)
		lines = append(lines, "!["+alt+"]("+image.URL+")")
	}
	return strings.Join(lines, "\n\n"), nil
}

func normalizeIssueInput(input IssueInput) (IssueInput, error) {
	input.Title = strings.TrimSpace(normalizeNewlines(input.Title))
	if input.Title == "" || len([]byte(input.Title)) > 256 ||
		strings.ContainsAny(input.Title, "\x00\r\n") {
		return IssueInput{}, errors.New("realqa github: issue title is invalid")
	}
	issueType, err := cleanIssueType(input.IssueType)
	if err != nil {
		return IssueInput{}, err
	}
	input.IssueType = issueType
	labelValues := make([]string, 0, len(input.Labels))
	for _, label := range input.Labels {
		labelValues = append(labelValues, label.Name)
	}
	labels, err := cleanStringValues(labelValues, 100)
	if err != nil {
		return IssueInput{}, err
	}
	input.Labels = make([]Label, 0, len(labels))
	for _, label := range labels {
		if err = ValidateLabelName(label); err != nil {
			return IssueInput{}, err
		}
		input.Labels = append(input.Labels, Label{Name: label})
	}
	assigneeValues := make([]string, 0, len(input.Assignees))
	for _, assignee := range input.Assignees {
		assigneeValues = append(assigneeValues, assignee.Login)
	}
	assignees, err := cleanStringValues(assigneeValues, 100)
	if err != nil {
		return IssueInput{}, err
	}
	input.Assignees = make([]Assignee, 0, len(assignees))
	for _, assignee := range assignees {
		if err = ValidateAssigneeLogin(assignee); err != nil {
			return IssueInput{}, err
		}
		input.Assignees = append(input.Assignees, Assignee{Login: assignee})
	}
	if input.Extension.Milestone != nil && input.Extension.Milestone.Number <= 0 {
		return IssueInput{}, errors.New("realqa github: milestone number is invalid")
	}
	if len(input.Extension.Projects) > 20 {
		return IssueInput{}, errors.New("realqa github: too many projects")
	}
	for _, project := range input.Extension.Projects {
		if ValidateProjectNodeID(project.NodeID) != nil ||
			(project.Permission != ProjectPermissionRepository &&
				project.Permission != ProjectPermissionOrganization) {
			return IssueInput{}, errors.New("realqa github: project extension is invalid")
		}
	}
	return input, nil
}

// ValidateLabelName applies the GitHub issue input boundary to one label.
func ValidateLabelName(value string) error {
	if strings.TrimSpace(value) != value || value == "" ||
		len([]byte(value)) > 255 || !utf8.ValidString(value) ||
		strings.ContainsAny(value, "\x00\r\n") ||
		utf8.RuneCountInString(value) > 50 {
		return errors.New("realqa github: label name is invalid")
	}
	return nil
}

// ValidateAssigneeLogin applies the GitHub issue input boundary to one login.
func ValidateAssigneeLogin(value string) error {
	if len([]byte(value)) > 255 || !utf8.ValidString(value) {
		return errors.New("realqa github: assignee login is invalid")
	}
	if _, err := cleanName(value); err != nil {
		return errors.New("realqa github: assignee login is invalid")
	}
	return nil
}

// ValidateProjectNodeID applies the GitHub issue input boundary to one project.
func ValidateProjectNodeID(value string) error {
	if !nodeIDPattern.MatchString(value) {
		return errors.New("realqa github: project node ID is invalid")
	}
	return nil
}

func escapeInline(value string) string {
	return strings.NewReplacer("\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`").
		Replace(value)
}
