// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"strings"
)

const ConfigurationNameConflictCause = "configuration_name_conflict"

// ConfigurationNameKey compares display names without rewriting stored spelling
// or resource identity. Default folding is locale independent; internal spaces
// remain significant. The second NFC joins sequences introduced by folding.
func ConfigurationNameKey(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(strings.TrimSpace(name))))
}

func ConfigurationNameConflict(kind Kind) *Error {
	noun := "project"
	if kind == RepositoryKind {
		noun = "repository"
	}
	problem := Fail(Conflict, "A "+noun+" with this name already exists.", "Choose another name.")
	problem.Cause = ConfigurationNameConflictCause
	return problem
}
