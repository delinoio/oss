package diagnostic

import "github.com/delinoio/oss/cmds/ttlc/internal/contracts"

type Diagnostic struct {
	Kind    contracts.DiagnosticKind `json:"kind"`
	Message string                   `json:"message"`
	Line    int                      `json:"line"`
	Column  int                      `json:"column"`
}
