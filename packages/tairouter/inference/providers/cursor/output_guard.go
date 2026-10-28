// SPDX-License-Identifier: Apache-2.0
// Prefix quarantine adapted from pinned MIT source; see LICENSE.upstream.
package cursor

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var routingTool = regexp.MustCompile(`(?i)\b(shell|read|grep|list|bash)\b|네이티브\s*(?:셸|쉘)`)
var routingHint = regexp.MustCompile(`(?i)\b(shell|read|grep|list|bash)\b|exec_command|shell_command|브리지|네이티브\s*(?:셸|쉘)`)
var routingFailure = regexp.MustCompile(`(?i)blocked|unavailable|interrupted|차단|중단|막혀`)
var routingRedirect = regexp.MustCompile(`(?i)exec_command|shell_command|\bexec\b|브리지|redirected|fallback|switch(?:ed|ing)?|전환|우회|통과(?:되|하)|다른\s*(?:도구|경로)|경로로`)

func falseRoutingClaim(text string) bool {
	if !routingFailure.MatchString(text) {
		return false
	}
	names := map[string]bool{}
	for _, span := range routingTool.FindAllStringSubmatchIndex(text, -1) {
		name := "shell"
		if span[2] >= 0 {
			name = strings.ToLower(text[span[2]:span[3]])
		} else if span[0] > 0 {
			previous, _ := utf8.DecodeLastRuneInString(text[:span[0]])
			if previous == '_' || unicode.IsLetter(previous) || unicode.IsMark(previous) || unicode.IsNumber(previous) {
				continue
			}
		}
		names[name] = true
	}
	return len(names) > 0 && (routingRedirect.MatchString(text) || len(names) >= 2)
}
func (s *Session) guardText(chunk string, final bool) (string, error) {
	if s.guardDone {
		return chunk, nil
	}
	if nativeModel(s.model) || s.sawRealTool {
		s.guardDone = true
		out := s.guardPending + chunk
		s.guardPending = ""
		return out, nil
	}
	text := s.guardPending + chunk
	// Never retain an unbounded frame in the quarantine. The decoder's frame cap
	// still bounds this immediate input; only the fixed prefix is inspected.
	probe := text
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	trimmed := strings.TrimLeft(probe, " \t\r\n")
	hold := false
	for _, marker := range []string{"[Tool Result]", "[Tool Error]", "[tool_result]"} {
		if !s.guardEcho {
			break
		}
		if strings.HasPrefix(trimmed, marker) {
			return "", protocolError("cursor_tool_result_echo")
		}
		if strings.HasPrefix(marker, trimmed) && len(text) <= 40 {
			hold = true
		}
	}
	if s.guardRouting {
		if falseRoutingClaim(probe) {
			return "", protocolError("cursor_routing_commentary_hallucination")
		}
		hint := routingHint.MatchString(probe)
		failure := routingFailure.MatchString(probe)
		lines := strings.Count(probe, "\n")
		if len(text) < 512 && (lines == 0 || hint && failure && lines < 2) && (hint || failure || len(text) < 64) {
			hold = true
		}
	}
	if hold && !final {
		s.guardPending = text
		return "", nil
	}
	s.guardPending = ""
	s.guardDone = true
	return text, nil
}
