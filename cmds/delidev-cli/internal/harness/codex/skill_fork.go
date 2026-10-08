// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

func (s *ForkSource) Checkpoint() ContinuationCheckpoint { return s.checkpoint }

// RehomeSkills makes complete child-owned package copies before any native child
// exists. Remapping is limited to explicit skill nodes and native generated path
// tags; user messages and unrelated opaque native history keep their meaning.
func (s *ForkSource) RehomeSkills(ctx context.Context, childHome string) (*ForkSource, error) {
	if err := s.Verify(ctx); err != nil {
		return nil, err
	}
	paths := map[string]string{}
	var collect func(any) error
	total := 0
	collect = func(value any) error {
		switch node := value.(type) {
		case []any:
			for _, item := range node {
				if err := collect(item); err != nil {
					return err
				}
			}
		case map[string]any:
			if node["type"] == "skill" {
				path, ok := node["path"].(string)
				if !ok || path == "" {
					return unsupportedFork()
				}
				if _, exists := paths[path]; !exists {
					if len(paths) >= skills.MaxInventory {
						return unsupportedFork()
					}
					copy, size, err := skills.CloneRuntimePackage(ctx, s.home, path, childHome)
					if err != nil {
						return err
					}
					total += size
					if total > skills.MaxTotalBytes {
						return unsupportedFork()
					}
					paths[path] = copy
				}
			}
			for _, item := range node {
				if err := collect(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, turn := range s.turns {
		var node any
		if json.Unmarshal(turn, &node) != nil {
			return nil, unsupportedFork()
		}
		if err := collect(node); err != nil {
			return nil, err
		}
	}
	if len(paths) == 0 {
		return s, nil
	}
	var rewrite func(any)
	rewrite = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, item := range node {
				rewrite(item)
			}
		case map[string]any:
			if node["type"] == "skill" {
				if path, ok := node["path"].(string); ok {
					if next, found := paths[path]; found {
						node["path"] = next
					}
				}
			}
			if text, ok := node["text"].(string); ok && strings.HasPrefix(strings.TrimSpace(text), "<skill>") {
				for source, target := range paths {
					text = strings.ReplaceAll(text, "<path>"+source+"</path>", "<path>"+target+"</path>")
				}
				node["text"] = text
			}
			for _, item := range node {
				rewrite(item)
			}
		}
	}
	transform := func(raw []byte) ([]byte, error) {
		var node any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&node) != nil {
			return nil, unsupportedFork()
		}
		rewrite(node)
		return json.Marshal(node)
	}
	result := *s
	result.original = s
	result.packageHome = childHome
	result.packages = paths
	result.turns = make([]json.RawMessage, len(s.turns))
	for i, turn := range s.turns {
		raw, err := transform(turn)
		if err != nil {
			return nil, err
		}
		result.turns[i] = raw
	}
	_, inputs, err := decodeLatestTurnInputs(marshalForkPage(result.turns[len(result.turns)-1:]))
	if err != nil || len(inputs) != len(s.checkpoint.Inputs) {
		return nil, continuationUncertain()
	}
	for i, input := range inputs {
		if input.ID != s.checkpoint.Inputs[i].ID || input.PromptDigest != s.checkpoint.Inputs[i].PromptDigest {
			return nil, continuationUncertain()
		}
	}
	result.checkpoint.Inputs = inputs
	result.home = filepath.Join(childHome, "fork-source")
	result.path = filepath.Join(result.home, "sessions", "selected-source.jsonl")
	if err := security.PrivateDir(result.home); err != nil {
		return nil, err
	}
	file, err := os.Open(s.path)
	if err != nil {
		return nil, continuationUncertain()
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxForkRollout+1))
	file.Close()
	if err != nil || len(raw) > maxForkRollout {
		return nil, continuationUncertain()
	}
	var rewritten bytes.Buffer
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		next, err := transform(line)
		if err != nil {
			return nil, err
		}
		rewritten.Write(next)
		rewritten.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(result.path), 0700); err != nil {
		return nil, err
	}
	if err := security.WriteAtomicOwned(result.path, rewritten.Bytes()); err != nil {
		return nil, err
	}
	result.fileDigest, err = forkRolloutDigest(ctx, result.home, result.path)
	if err != nil {
		return nil, err
	}
	if err := s.Verify(ctx); err != nil {
		return nil, err
	}
	return &result, nil
}

func equivalentForkJSON(a, b json.RawMessage) bool {
	decode := func(raw json.RawMessage) []byte {
		var value any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if d.Decode(&value) != nil {
			return nil
		}
		result, _ := json.Marshal(value)
		return result
	}
	left, right := decode(a), decode(b)
	return left != nil && right != nil && bytes.Equal(left, right)
}
