package diagnostic

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/ttlc/internal/contracts"
)

type Diagnostic struct {
	Kind    contracts.DiagnosticKind `json:"kind"`
	Message string                   `json:"message"`
	Line    int                      `json:"line"`
	Column  int                      `json:"column"`
}

func (d Diagnostic) DeterministicID(sourcePath string) string {
	normalizedSourcePath := filepath.ToSlash(strings.TrimSpace(sourcePath))
	fingerprint := strings.Join([]string{
		string(d.Kind),
		normalizedSourcePath,
		strconv.Itoa(d.Line),
		strconv.Itoa(d.Column),
		d.Message,
	}, "|")
	sum := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(sum[:])
}
