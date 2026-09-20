package core

import (
	"bytes"
	"encoding/json"
	"strings"
)

type goReportIdentity struct {
	build bool
	name  [2]string
}

type goFailureSite struct {
	identity goReportIdentity
	line     int
}

func parseGoTest(b []byte, check, command, logID string) ([]Failure, error) {
	failures, _, err := parseGoTestSummaries(b, check, command, logID, nil)
	return failures, err
}

func parseGoTestSummaries(b []byte, check, command, logID string, secrets []string) ([]Failure, bool, error) {
	out := reportFailures{secrets: secrets}
	seen, terminal := false, false
	output := map[[2]string]*reportOutputTail{}
	buildOutput := map[string]*reportOutputTail{}
	tests := map[[2]string]struct{}{}
	var sites []goFailureSite
	retain := func(identity goReportIdentity, line int, test, message string) {
		before := len(out.failures)
		// The second pass fills the original SHA-256 identity. Reserve its exact
		// encoded size while enforcing the summary budget in the first pass.
		out.add(Failure{ID: strings.Repeat("0", 64), Check: check, Test: test, Command: command, Message: message, LogID: logID})
		if len(out.failures) != before {
			sites = append(sites, goFailureSite{identity, line})
		}
		if out.full {
			output, buildOutput = nil, nil
		}
	}
	lineNumber := 0
	// Slice the bounded input rather than allocating a second event buffer.
	for line := range bytes.SplitSeq(b, []byte{'\n'}) {
		lineNumber++
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event struct{ Action, Package, Test, Output, ImportPath string }
		if err := json.Unmarshal(line, &event); err != nil || event.Action == "" {
			return nil, false, E("report-malformed", "invalid Go test JSON event", 1)
		}
		seen = true
		key := [2]string{event.Package, event.Test}
		switch event.Action {
		case "build-output", "build-fail":
			if event.ImportPath == "" {
				return nil, false, E("report-malformed", "Go build event has no import path", 1)
			}
			if event.Action == "build-output" {
				if !out.full {
					if buildOutput[event.ImportPath] == nil {
						buildOutput[event.ImportPath] = &reportOutputTail{}
					}
					buildOutput[event.ImportPath].append(event.Output)
				}
			} else {
				terminal = true
				if !out.full {
					retain(goReportIdentity{true, [2]string{event.ImportPath, ""}}, lineNumber, event.ImportPath, strings.TrimSpace(buildOutput[event.ImportPath].String()))
				}
				delete(buildOutput, event.ImportPath)
			}
		case "output":
			if !out.full {
				if output[key] == nil {
					output[key] = &reportOutputTail{}
				}
				output[key].append(event.Output)
			}
		case "run", "start":
			tests[key] = struct{}{}
		case "pass", "skip", "fail":
			terminal = true
			delete(tests, key)
			if event.Action == "fail" && !out.full {
				retain(goReportIdentity{false, key}, lineNumber, event.Package+"/"+event.Test, strings.TrimSpace(output[key].String()))
			}
			delete(output, key)
		case "pause", "cont", "bench":
		default:
			return nil, false, E("report-malformed", "unknown Go test JSON action", 1)
		}
	}
	if !seen || !terminal {
		return nil, false, E("report-malformed", "Go test report is empty, truncated or incomplete", 1)
	}
	if len(tests) != 0 {
		return nil, false, E("report-incomplete", "Go test report contains unfinished tests", 1)
	}
	assignGoFailureIDs(b, check, sites, out.failures)
	return out.failures, out.truncated, nil
}

// Count completed iterations only for identities in the bounded summary prefix.
// A second scan of the already validated bytes preserves pass/skip history and
// repeated build/test IDs without a map of every completed test or disk spill of
// unredacted report data. Stop at the final retained failure.
func assignGoFailureIDs(b []byte, check string, sites []goFailureSite, failures []Failure) {
	counts := make(map[goReportIdentity]int, len(sites))
	for _, site := range sites {
		counts[site.identity] = 0
	}
	next, lineNumber := 0, 0
	for line := range bytes.SplitSeq(b, []byte{'\n'}) {
		if next == len(sites) {
			return
		}
		lineNumber++
		var event struct{ Action, Package, Test, ImportPath string }
		_ = json.Unmarshal(line, &event) // The first pass validated every event.
		identity := goReportIdentity{false, [2]string{event.Package, event.Test}}
		switch event.Action {
		case "build-fail":
			identity = goReportIdentity{true, [2]string{event.ImportPath, ""}}
		case "pass", "skip", "fail":
		default:
			continue
		}
		count, retained := counts[identity]
		if !retained {
			continue
		}
		count++
		counts[identity] = count
		if lineNumber == sites[next].line {
			if identity.build {
				failures[next].ID = Hash(Encode([]any{check, "go-build", identity.name[0], count}))
			} else {
				failures[next].ID = Hash(Encode([]any{check, "go", identity.name, count}))
			}
			next++
		}
	}
}
