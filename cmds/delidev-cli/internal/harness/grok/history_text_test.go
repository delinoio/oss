package grok

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func textHistoryFixture(t *testing.T) (*apiConnection, map[string][]byte, string) {
	t.Helper()
	input := "Original retained input.\n한글 + <literal>"
	chunk, err := parseTextChunk(textChunkFixture, turnFixtureSession, turnFixturePrompt)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := parseTurnCompleted(turnCompletedFixture, turnFixtureSession, turnFixturePrompt, turnFixtureModel)
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := TextInputClaimDigest(turnFixtureSession, input)
	usage, _ := validateUsage(turn.Update.Usage, turnFixtureModel)
	completed := &completedText{request: domain.NewID(), prompt: turnFixturePrompt, summary: "Original retained summary.", summarySeen: true, idle: true, bodyDigest: digest, chunks: [][32]byte{historyValueDigest(chunk)}, lastChunk: historyTextEnvelopeDigest(chunk), output: sha256.Sum256([]byte(chunk.Update.Content.Text)), terminal: historyValueDigest(turn), usage: usage, closed: domain.NewID()}
	home := historyFileFixture(t, nil)
	workspace := filepath.Join(filepath.Dir(home), "workspace 공백_+.-()")
	completed.home, _ = os.Lstat(home)
	a := &apiConnection{gate: make(chan struct{}, 1), profile: apiProfile{model: turnFixtureModel, path: filepath.Join(home, "config.toml")}, workspace: workspace, session: turnFixtureSession, completedText: completed}
	files := map[string][]byte{}
	encode := func(value any) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	lines := func(values ...any) []byte {
		var out []byte
		for _, value := range values {
			out = append(out, encode(value)...)
			out = append(out, '\n')
		}
		return out
	}
	user := storedUser{Session: turnFixtureSession}
	user.Update.Kind, user.Update.Content, user.Update.Meta.Model = "user_message_chunk", promptText{Type: "text", Text: input}, turnFixtureModel
	user.Meta.Event, user.Meta.TimestampMS = string(turnFixtureSession)+"-2", chunk.Meta.TimestampMS-1
	row := func(method string, stamp uint64, value any) any {
		return map[string]any{"timestamp": stamp / 1000, "method": method, "params": value}
	}
	files["updates.jsonl"] = lines(row("session/update", user.Meta.TimestampMS, user), row("session/update", chunk.Meta.TimestampMS, chunk), row("_x.ai/session/update", turn.Meta.TimestampMS, turn))
	files["system_prompt.txt"] = []byte("Synthetic system fixture, not a native prompt.")
	files["chat_history.jsonl"] = lines(
		map[string]any{"type": "system", "content": string(files["system_prompt.txt"])},
		map[string]any{"type": "user", "content": []promptText{{Type: "text", Text: "Synthetic context."}}},
		map[string]any{"type": "user", "content": []promptText{{Type: "text", Text: "Synthetic reminder."}}, "synthetic_reason": "system_reminder"},
		map[string]any{"type": "user", "content": []promptText{{Type: "text", Text: "<user_query>\n" + input + "\n</user_query>"}}, "prompt_index": 0},
		map[string]any{"type": "assistant", "content": chunk.Update.Content.Text, "model_id": turnFixtureModel},
	)
	counters := storedCounters{usage.Input, usage.Output, usage.CachedRead, usage.CacheCreation, usage.Reasoning, usage.Total, usage.Calls}
	nativeUsage := storedUsage{Input: usage.Input, Output: usage.Output, CachedRead: usage.CachedRead, CacheCreation: usage.CacheCreation, Reasoning: usage.Reasoning, Total: usage.Total, Calls: usage.Calls, Turns: 1, Model: turnFixtureModel, Models: encode(map[string]any{turnFixtureModel: counters})}
	stamp := "2026-09-28T00:00:00.123+00:00"
	turnUsage := nativeUsage
	number := uint64(1)
	turnUsage.Number, turnUsage.Ended = &number, &stamp
	files["usage.json"] = encode(map[string]any{"sessionId": turnFixtureSession, "updatedAt": stamp, "session": nativeUsage, "turns": []storedUsage{turnUsage}})
	summary := textSummary{Agent: "ag1.ec802919a69144ac95d20166eed2f9b9", Attempt: "at1.5b3206bcc32b4ac8ab37ef4cb9fe89e4", Summary: input, Created: stamp, Updated: stamp, Messages: 3, ChatMessages: 5, Model: "delidev-selected", NextTurn: 1, Format: 1, Request: turnFixturePrompt, Home: home, Active: stamp, Title: input, AgentName: "grok-build-plan", Sandbox: "off", LastSummary: completed.summary, LastPrompt: turnFixturePrompt}
	summary.Info.Session, summary.Info.Cwd = turnFixtureSession, workspace
	files["summary.json"] = encode(summary)
	base, err := historySessionPath(workspace, turnFixtureSession)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(home, base)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	writeHistoryFixture(t, directory, files)
	return a, files, directory
}

func writeHistoryFixture(t *testing.T, directory string, files map[string][]byte) {
	t.Helper()
	for name, raw := range files {
		if err := os.WriteFile(filepath.Join(directory, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClosedTextHistoryMatchesOriginalWithoutMutationOrAuthority(t *testing.T) {
	a, files, directory := textHistoryFixture(t)
	var logs bytes.Buffer
	a.inspection.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	first, err := a.verifyClosedText(context.Background())
	if err != nil || first.InputID != a.completedText.request || first.ClosureID != a.completedText.closed || first.TextChunks != 1 || len(first.FilesDigest) != 64 {
		t.Fatal("original retained text not verified", err)
	}
	second, err := a.verifyClosedText(context.Background())
	if err != nil || second != first {
		t.Fatal("read-only verification changed proof", err)
	}
	for name, original := range files {
		raw, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || !bytes.Equal(raw, original) {
			t.Fatal("history reader changed native state")
		}
	}
	for _, private := range []string{"retained input", "retained summary", "fixture response", directory, a.workspace, turnFixtureModel} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("history diagnostic leaked private content")
		}
	}
	encoded, _ := json.Marshal(first)
	if bytes.Contains(encoded, []byte("content")) || bytes.Contains(encoded, []byte(a.workspace)) {
		t.Fatal("comparison exported native content")
	}
}

func TestClosedTextHistoryRejectsOriginalEvidenceDrift(t *testing.T) {
	for _, name := range []string{"input", "chunk", "terminal", "chat", "system", "usage", "summary", "foreign-session", "foreign-prompt", "unknown-record", "reordered", "truncated", "closure-missing", "original-home-replaced", "canceled"} {
		t.Run(name, func(t *testing.T) {
			a, files, directory := textHistoryFixture(t)
			ctx := context.Background()
			replace := func(file, old, new string) {
				if !bytes.Contains(files[file], []byte(old)) {
					t.Fatal("mutation target absent", file, old)
				}
				files[file] = bytes.ReplaceAll(files[file], []byte(old), []byte(new))
			}
			switch name {
			case "input":
				replace("updates.jsonl", "Original retained input", "Changed retained input")
			case "chunk":
				replace("updates.jsonl", "Private fixture response.", "Changed fixture response.")
			case "terminal":
				replace("updates.jsonl", `"elapsed_ms":324`, `"elapsed_ms":325`)
			case "chat":
				replace("chat_history.jsonl", "Private fixture response.", "Changed fixture response.")
			case "system":
				files["system_prompt.txt"] = []byte("Changed system fixture.")
			case "usage":
				replace("usage.json", `"inputTokens":11`, `"inputTokens":12`)
			case "summary":
				replace("summary.json", "Original retained summary.", "Changed retained summary.")
			case "foreign-session":
				replace("updates.jsonl", string(turnFixtureSession), string(domain.NewID()))
			case "foreign-prompt":
				replace("updates.jsonl", turnFixturePrompt, "2aed0867-746a-4425-8860-b41c9c0c0b5c")
			case "unknown-record":
				files["updates.jsonl"] = append(files["updates.jsonl"], []byte("{}\n")...)
			case "reordered":
				rows := bytes.Split(files["updates.jsonl"], []byte("\n"))
				rows[0], rows[1] = rows[1], rows[0]
				files["updates.jsonl"] = bytes.Join(rows, []byte("\n"))
			case "truncated":
				files["updates.jsonl"] = bytes.TrimSuffix(files["updates.jsonl"], []byte("\n"))
			case "closure-missing":
				a.completedText.closed = ""
			case "original-home-replaced":
				home := filepath.Dir(a.profile.path)
				if err := os.Rename(home, home+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			writeHistoryFixture(t, directory, files)
			proof, err := a.verifyClosedText(ctx)
			if err == nil || proof != (TextHistory{}) || domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("changed native evidence accepted", err)
			}
		})
	}
}

func TestClosedTextHistoryRetainsFirstFileDigest(t *testing.T) {
	a, files, directory := textHistoryFixture(t)
	original, err := a.verifyClosedText(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Formatting is semantically equivalent, but later reads cannot silently
	// replace the original file snapshot after a proof was already returned.
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, files["summary.json"], "", "  "); err != nil {
		t.Fatal(err)
	}
	files["summary.json"] = formatted.Bytes()
	writeHistoryFixture(t, directory, files)
	if proof, err := a.verifyClosedText(context.Background()); err == nil || proof != (TextHistory{}) {
		t.Fatal("original file snapshot changed")
	}
	if *a.completedText.history != original {
		t.Fatal("failed comparison replaced original proof")
	}
}

func TestHistoryPathEncodesNativeWorkspaceBytes(t *testing.T) {
	workspace := filepath.Join(string(filepath.Separator), "workspace 공백_+.-()")
	path, err := historySessionPath(workspace, turnFixtureSession)
	if err != nil || !strings.Contains(path, "workspace%20%EA%B3%B5%EB%B0%B1_%2B.-%28%29") {
		t.Fatal("native path encoding drift", err)
	}
	for _, bad := range []string{"relative", "../grok", workspace + string(filepath.Separator) + ".", workspace + "\x00"} {
		if _, err := historySessionPath(bad, turnFixtureSession); err == nil {
			t.Fatal("unsafe workspace accepted")
		}
	}
}

func TestRetainedHistorySchemasRejectNestedAliasesAndNull(t *testing.T) {
	a, files, _ := textHistoryFixture(t)
	profiles := []struct {
		name   string
		raw    []byte
		verify func([]byte) error
	}{
		{"usage", files["usage.json"], func(raw []byte) error { return verifyTextUsage(raw, a.session, a.profile.model, a.completedText.usage) }},
		{"summary", files["summary.json"], func(raw []byte) error {
			return verifyTextSummary(raw, filepath.Dir(a.profile.path), a.workspace, a.session, a.completedText)
		}},
	}
	for _, profile := range profiles {
		root := fixtureObject(profile.raw)
		var inspect func(any, string)
		inspect = func(value any, path string) {
			switch node := value.(type) {
			case map[string]any:
				keys := make([]string, 0, len(node))
				for key := range node {
					keys = append(keys, key)
				}
				for _, key := range keys {
					child := node[key]
					for _, mode := range []string{"alias", "null"} {
						t.Run(profile.name+path+"/"+key+"/"+mode, func(t *testing.T) {
							if mode == "alias" {
								node[strings.ToUpper(key)] = child
							} else {
								node[key] = nil
							}
							raw, _ := json.Marshal(root)
							if mode == "alias" {
								delete(node, strings.ToUpper(key))
							} else {
								node[key] = child
							}
							if profile.verify(raw) == nil {
								t.Fatal("invalid native history shape accepted")
							}
						})
					}
					inspect(child, path+"/"+key)
				}
			case []any:
				for _, child := range node {
					inspect(child, path+"/item")
				}
			}
		}
		inspect(root, "")
	}
}

func TestHistoryUsagePreservesExactCountersAndMissingDuration(t *testing.T) {
	a, files, _ := textHistoryFixture(t)
	raw := bytes.ReplaceAll(files["usage.json"], []byte(`"totalTokens":16`), []byte(`"totalTokens":9007199254740993`))
	expected := a.completedText.usage
	expected.Total = 9007199254740993
	if err := verifyTextUsage(raw, a.session, a.profile.model, expected); err != nil {
		t.Fatal("persisted counter rounded", err)
	}
	for _, replacement := range []string{`"totalTokens":9007199254740992`, `"totalTokens":null`, `"totalTokens":-1`, `"totalTokens":1.5`, `"totalTokens":18446744073709551616`, `"totalTokens":9007199254740993,"apiDurationMs":5`} {
		changed := bytes.ReplaceAll(raw, []byte(`"totalTokens":9007199254740993`), []byte(replacement))
		if verifyTextUsage(changed, a.session, a.profile.model, expected) == nil {
			t.Fatal("unknown or changed counters accepted")
		}
	}
}

func TestOriginalTextFootprintIsIndependentOfPublicationCopies(t *testing.T) {
	config, _ := fixtureAPIConfig(t, "closure-valid")
	config.Model = turnFixtureModel
	api, err := openAPI(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	if _, err := api.Create(context.Background(), domain.NewID(), domain.NewID(), func(context.Context, CreationClaim) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := api.RunText(context.Background(), domain.NewID(), "Original input.", func(context.Context, InputClaim) error { return nil }, func(_ context.Context, observation InputObservation) error {
		if observation.Chunk != nil {
			observation.Chunk.Update.Content.Text = "Mutated callback copy."
		}
		if observation.Result != nil {
			observation.Result.Meta.Usage.Input = 999
			observation.Result.Meta.Prompt = "mutated"
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	chunk, _ := parseTextChunk(textChunkFixture, turnFixtureSession, turnFixturePrompt)
	original := api.completedText
	if len(original.chunks) != 1 || original.chunks[0] != historyValueDigest(chunk) || original.output != sha256.Sum256([]byte(chunk.Update.Content.Text)) || original.usage.Input != 11 || original.prompt != turnFixturePrompt {
		t.Fatal("callback replaced original retained evidence")
	}
	if _, err := api.verifyClosedText(context.Background()); err == nil {
		t.Fatal("unclosed input acquired history proof")
	}
}

func TestHistoryOmittedMetadataCannotBecomeExplicitEmptyFields(t *testing.T) {
	a, files, _ := textHistoryFixture(t)
	usage := fixtureObject(files["usage.json"])
	usage["session"].(map[string]any)["endedAt"] = ""
	raw, _ := json.Marshal(usage)
	if verifyTextUsage(raw, a.session, a.profile.model, a.completedText.usage) == nil {
		t.Fatal("session acquired empty turn-only timestamp")
	}
	rows := bytes.Split(files["chat_history.jsonl"], []byte("\n"))
	row := fixtureObject(rows[1])
	row["synthetic_reason"] = ""
	rows[1], _ = json.Marshal(row)
	input, err := verifyTextUpdates(files["updates.jsonl"], a.session, a.profile.model, a.completedText)
	if err != nil {
		t.Fatal(err)
	}
	if verifyTextChat(bytes.Join(rows, []byte("\n")), files["system_prompt.txt"], input, a.profile.model, a.completedText) == nil {
		t.Fatal("context acquired empty reminder metadata")
	}
}

func TestNativeHistoryCoalescesTextWithLastOriginalMetadata(t *testing.T) {
	a, files, directory := textHistoryFixture(t)
	first, _ := parseTextChunk(textChunkFixture, a.session, a.completedText.prompt)
	first.Update.Content.Text = strings.Repeat("a", 200<<10)
	last := first
	last.Update.Content.Text = strings.Repeat("b", 200<<10)
	last.Meta.Chunk++
	last.Meta.Event = string(a.session) + "-5"
	last.Meta.TimestampMS++
	merged := last
	merged.Update.Content.Text = first.Update.Content.Text + last.Update.Content.Text
	a.completedText.chunks = [][32]byte{historyValueDigest(first), historyValueDigest(last)}
	a.completedText.lastChunk = historyTextEnvelopeDigest(last)
	a.completedText.output = sha256.Sum256([]byte(merged.Update.Content.Text))
	rows := bytes.Split(files["updates.jsonl"], []byte("\n"))
	row := fixtureObject(rows[1])
	row["params"] = merged
	rows[1], _ = json.Marshal(row)
	files["updates.jsonl"] = bytes.Join(rows, []byte("\n"))
	chat := bytes.Split(files["chat_history.jsonl"], []byte("\n"))
	assistant := fixtureObject(chat[4])
	assistant["content"] = merged.Update.Content.Text
	chat[4], _ = json.Marshal(assistant)
	files["chat_history.jsonl"] = bytes.Join(chat, []byte("\n"))
	writeHistoryFixture(t, directory, files)
	if proof, err := a.verifyClosedText(context.Background()); err != nil || proof.TextChunks != 2 {
		t.Fatal("merged native history lost live chunk provenance", err)
	}
	// Earlier metadata with the same complete text is not the observed native
	// coalescing result; the last original chunk remains independently required.
	merged.Meta = first.Meta
	row["params"] = merged
	rows[1], _ = json.Marshal(row)
	if _, err := verifyTextUpdates(bytes.Join(rows, []byte("\n")), a.session, a.profile.model, a.completedText); err == nil {
		t.Fatal("merged history substituted earlier metadata")
	}
}

func TestNativeHistoryTraceIDsPreserveUnpaddedUUIDs(t *testing.T) {
	for _, prefix := range []string{"ag1.", "at1."} {
		for _, suffix := range []string{"9fc73ce04b34f058af045eb3d02d686", "ec802919a69144ac95d20166eed2f9b9", "40008000000000000000"} {
			if !nativeTraceID(prefix+suffix, prefix) {
				t.Fatal("native unpadded UUID rejected")
			}
		}
		for _, suffix := range []string{"", "0", "09fc73ce04b34f058af045eb3d02d686", "9FC73CE04B34F058AF045EB3D02D686", "ec802919a69174ac95d20166eed2f9b9", "ec802919a69144acf5d20166eed2f9b9", strings.Repeat("a", 33), "ffffffffffffffffffffffffffffffff"} {
			if nativeTraceID(prefix+suffix, prefix) {
				t.Fatal("malformed native trace ID accepted")
			}
		}
	}
	a, files, directory := textHistoryFixture(t)
	files["summary.json"] = bytes.ReplaceAll(files["summary.json"], []byte("ag1.ec802919a69144ac95d20166eed2f9b9"), []byte("ag1.9fc73ce04b34f058af045eb3d02d686"))
	writeHistoryFixture(t, directory, files)
	if _, err := a.verifyClosedText(context.Background()); err != nil {
		t.Fatal("original native short trace ID blocked history", err)
	}
}
