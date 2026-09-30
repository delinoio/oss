// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"io"
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func readDocument(path string, input io.Reader) ([]byte, error) {
	reader := input
	if path == "-" && terminalInput(input) {
		return nil, domain.Fail(domain.MissingInput, "A JSON document is required.", "Pipe the document through stdin or pass --input PATH.")
	}
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, domain.Fail(domain.InvalidArgument, "The input document could not be read.", "Provide a readable JSON file or use stdin.")
		}
		defer file.Close()
		reader = file
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil, domain.Fail(domain.InvalidArgument, "The input document exceeds its bound or is unreadable.", "Provide at most 1 MiB of UTF-8 JSON.")
	}
	return raw, nil
}
