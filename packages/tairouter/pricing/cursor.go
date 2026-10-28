// SPDX-License-Identifier: Apache-2.0
package pricing

import (
	"regexp"
	"strings"
)

var anthropicBase = regexp.MustCompile(`^claude-(fable|haiku|opus|sonnet)-(\d+(?:[.-]\d+)*)$`)
var versionBase = regexp.MustCompile(`^claude-(\d+(?:\.\d+)*)-(fable|haiku|opus|sonnet)$`)
var cursorPatterns = []struct {
	re                    *regexp.Regexp
	thinking, fast, level bool
}{
	{regexp.MustCompile(`^(.*)-thinking-([a-z-]+)-fast$`), true, true, true},
	{regexp.MustCompile(`^(.*)-([a-z-]+)-thinking-fast$`), true, true, true},
	{regexp.MustCompile(`^(.*)-thinking-([a-z-]+)$`), true, false, true},
	{regexp.MustCompile(`^(.*)-([a-z-]+)-thinking$`), true, false, true},
	{regexp.MustCompile(`^(.*)-([a-z-]+)-fast$`), false, true, true},
	{regexp.MustCompile(`^(.*)-thinking-fast$`), true, true, false},
	{regexp.MustCompile(`^(.*)-thinking$`), true, false, false},
	{regexp.MustCompile(`^(.*)-fast$`), false, true, false},
}

func parseCursorBase(id string) string {
	var family, version string
	if m := anthropicBase.FindStringSubmatch(id); m != nil {
		family = m[1]
		version = strings.ReplaceAll(m[2], ".", "-")
	} else if m := versionBase.FindStringSubmatch(id); m != nil {
		family = m[2]
		version = strings.ReplaceAll(m[1], ".", "-")
	} else {
		return ""
	}
	vf := "claude-" + strings.ReplaceAll(version, "-", ".") + "-" + family
	switch vf {
	case "claude-4.5-haiku", "claude-4.5-opus", "claude-4.6-opus", "claude-4.5-sonnet", "claude-4.6-sonnet", "claude-4-sonnet":
		return vf
	}
	return "claude-" + family + "-" + version
}
func cursorModel(provider, model string) (base string, fast, supported bool) {
	if provider != "cursor" {
		return "", false, true
	}
	id := strings.ToLower(strings.TrimSpace(model))
	for _, p := range cursorPatterns {
		m := p.re.FindStringSubmatch(id)
		if m == nil {
			continue
		}
		level := ""
		if p.level {
			level = m[2]
			switch level {
			case "none", "minimal", "low", "medium", "high", "xhigh", "max", "extra-high":
			default:
				continue
			}
		}
		b := parseCursorBase(m[1])
		if b == "" {
			continue
		}
		if !p.fast {
			return b, false, true
		}
		switch b {
		case "claude-opus-4-8", "claude-opus-5", "claude-opus-5-5":
		default:
			return b, true, false
		}
		if b == "claude-opus-5-5" && p.thinking {
			return b, true, false
		}
		if level != "" && level != "low" && level != "medium" && level != "high" && level != "xhigh" && level != "max" {
			return b, true, false
		}
		if b == "claude-opus-5" && !p.thinking && (level == "xhigh" || level == "max") {
			return b, true, false
		}
		return b, true, true
	}
	return parseCursorBase(id), false, true
}
