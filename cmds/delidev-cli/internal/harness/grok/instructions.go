package grok

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Grok 1.0.41 discovers this additive global rules file inside the fresh,
// execution-owned GROK_HOME. Instructions never replace its base system prompt
// or enter argv, environment, logs, or the user's workspace.
type instructionProfile struct {
	path     string
	contents string
}

func (p instructionProfile) write() error {
	if p.contents == "" {
		return p.check()
	}
	file, err := os.OpenFile(p.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return apiConfigurationError()
	}
	_, writeErr := file.WriteString(p.contents)
	syncErr, closeErr := file.Sync(), file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || security.SyncParent(p.path) != nil {
		return apiConfigurationError()
	}
	return p.check()
}

func (p instructionProfile) check() error {
	if p.contents == "" {
		if _, err := os.Lstat(p.path); !errors.Is(err, os.ErrNotExist) {
			return incompatible()
		}
		return nil
	}
	raw, err := security.ReadPrivate(p.path, domain.MaxAppliedInstructions)
	if err != nil || !bytes.Equal(raw, []byte(p.contents)) {
		return incompatible()
	}
	return nil
}

func (p instructionProfile) inspect(raw []byte) error {
	if p.contents == "" {
		if !emptyArray(raw) {
			return incompatible()
		}
		return nil
	}
	var instructions []struct {
		Path   string  `json:"path"`
		Scope  string  `json:"scope"`
		Type   string  `json:"fileType"`
		Bytes  *uint64 `json:"sizeBytes"`
		Tokens *uint64 `json:"approxTokens"`
	}
	if decode(raw, &instructions) != nil || len(instructions) != 1 {
		return incompatible()
	}
	i := instructions[0]
	if i.Path != p.path || i.Scope != "global" || i.Type != "agents_md" || i.Bytes == nil || *i.Bytes != uint64(len(p.contents)) || i.Tokens == nil || *i.Tokens > domain.MaxAppliedInstructions {
		return incompatible()
	}
	return nil
}

func (p instructionProfile) digest() string {
	if p.contents == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(p.contents))
	return hex.EncodeToString(digest[:])
}

// The pinned native writer appends the complete global rule as its final user
// rule in the original context row. Compare the exact native envelope and bytes;
// merely finding a marker somewhere in history does not establish application.
func (p instructionProfile) verifyContext(content string) bool {
	if p.contents == "" {
		return true
	}
	// Native delimiters always start on a new line, including when the exact
	// source file has no final newline. The file itself remains unchanged.
	body := p.contents
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return strings.HasSuffix(content, "\n\n<user_rule>\n"+body+"</user_rule>\n</user_rules>\n</rules>")
}
