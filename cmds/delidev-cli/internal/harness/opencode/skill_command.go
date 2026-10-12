// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

// SkillCommand is private immutable preflight data. Only the original selected
// binding can create it. Manually typed tokens cannot enter this boundary.
type SkillCommand struct {
	Name            string
	Location        string
	Arguments       string `json:"-"`
	ContentRevision string
	Text            string `json:"-"`
}

var nativeCommandArguments = regexp.MustCompile(`(?:\[Image\s+\d+\]|"[^"]*"|'[^']*'|[^\s"']+)`)
var nativeCommandPlaceholder = regexp.MustCompile(`\$(\d+)`)

// Native command input supports exactly one invocation per original user input.
// Multiple selected package roots remain available for native automatic use.
// Refuse unsupported invocation shapes before claiming or opening a listener.
func PrepareSkillCommand(input domain.SessionInput, selected []skills.OpenCodeSelection) (*SkillCommand, error) {
	if len(input.Skills) == 0 {
		return nil, nil
	}
	if len(input.Skills) != 1 || len(input.Attachments) != 0 {
		return nil, incompatible()
	}
	binding := input.Skills[0]
	var packageSelection *skills.OpenCodeSelection
	for i := range selected {
		if selected[i].ContentRevision == binding.ContentRevision {
			token := "$" + selected[i].Name
			if input.Prompt == token || strings.HasPrefix(input.Prompt, token+" ") || strings.HasPrefix(input.Prompt, token+"\n") || strings.HasPrefix(input.Prompt, token+"\t") {
				if packageSelection != nil {
					return nil, incompatible()
				}
				packageSelection = &selected[i]
			}
		}
	}
	if packageSelection == nil {
		return nil, incompatible()
	}
	token := "$" + packageSelection.Name
	arguments := input.Prompt[len(token):]
	if len(arguments) > 0 {
		arguments = arguments[1:]
	} // Remove only the selected token separator.
	return nativeSkillCommand(*packageSelection, arguments)
}

// This mirrors the pinned native command template solely for comparison. The
// resulting text is never submitted through prompt_async; native command owns
// the actual expansion and invocation identity.
func nativeSkillCommand(selection skills.OpenCodeSelection, arguments string) (*SkillCommand, error) {
	if domain.Text(arguments, "native skill arguments", 256<<10, false) != nil {
		return nil, sessionInvalid()
	}
	template := selection.Content + "\n\nBase directory for this skill: " + filepath.Dir(selection.Path) + "\nRelative paths in this skill (e.g., scripts/, references/) are relative to this base directory."
	// These native transformations can create extra files or execute shell before
	// the original message is stored. No safe atomic receipt profile exists for
	// them here. Preserve package bytes and fail before native input.
	if strings.Contains(template, "!`") || strings.ContainsAny(template, "@") || strings.ContainsAny(arguments, "@") {
		return nil, incompatible()
	}
	rawArgs := nativeCommandArguments.FindAllString(arguments, -1)
	args := make([]string, len(rawArgs))
	for i, arg := range rawArgs {
		// Native /^['"]|['"]$/g strips one quote at each boundary. Preserve
		// inner quotes and ordered arguments exactly as the native parser does.
		if strings.HasPrefix(arg, "\"") || strings.HasPrefix(arg, "'") {
			arg = arg[1:]
		}
		if strings.HasSuffix(arg, "\"") || strings.HasSuffix(arg, "'") {
			arg = arg[:len(arg)-1]
		}
		args[i] = arg
	}
	last := 0
	for _, match := range nativeCommandPlaceholder.FindAllStringSubmatch(template, -1) {
		number, err := strconv.Atoi(match[1])
		if err != nil || number < 1 {
			return nil, incompatible()
		}
		if number > last {
			last = number
		}
	}
	text := nativeCommandPlaceholder.ReplaceAllStringFunc(template, func(match string) string {
		number, _ := strconv.Atoi(match[1:])
		index := number - 1
		if index < 0 || index >= len(args) {
			return ""
		}
		if number == last {
			return strings.Join(args[index:], " ")
		}
		return args[index]
	})
	usesArguments := strings.Contains(template, "$ARGUMENTS")
	text = strings.ReplaceAll(text, "$ARGUMENTS", arguments)
	if last == 0 && !usesArguments && strings.TrimSpace(arguments) != "" {
		text += "\n\n" + arguments
	}
	// Native expansions run after argument interpolation. Arguments or adjacent
	// placeholders can synthesize a shell marker absent from the original package.
	// Reject the final native bytes before any original claim or send is created.
	if strings.Contains(text, "!`") || strings.Contains(text, "@") {
		return nil, incompatible()
	}
	text = strings.TrimSpace(text)
	if domain.Text(text, "native skill input", 256<<10, true) != nil {
		return nil, sessionInvalid()
	}
	if domain.Text(text, "native command text", domain.MaxMessageText, false) != nil {
		return nil, incompatible()
	}
	return &SkillCommand{Name: selection.Name, Location: selection.Path, Arguments: arguments, ContentRevision: selection.ContentRevision, Text: text}, nil
}

func encodeSkillCommand(settings SessionSettings, message string, command SkillCommand) ([]byte, error) {
	if !validSessionSettings(settings) || !nativeID(message, "msg") || command.Name == "" || command.ContentRevision == "" {
		return nil, sessionInvalid()
	}
	raw, err := json.Marshal(struct {
		MessageID string       `json:"messageID"`
		Agent     PrimaryAgent `json:"agent"`
		Model     string       `json:"model"`
		Command   string       `json:"command"`
		Arguments string       `json:"arguments"`
	}{message, settings.Agent, settings.Provider + "/" + settings.Model, command.Name, command.Arguments})
	if err != nil || len(raw) > maxHTTPBody {
		return nil, sessionInvalid()
	}
	return raw, nil
}

// SkillCommandClaimDigest compares the one native command body with immutable
// assignment evidence. It never derives a permission from native prompt text.
func SkillCommandClaimDigest(settings SessionSettings, message string, command SkillCommand) (string, error) {
	raw, err := encodeSkillCommand(settings, message, command)
	if err != nil {
		return "", err
	}
	return mutationDigest(raw), nil
}
