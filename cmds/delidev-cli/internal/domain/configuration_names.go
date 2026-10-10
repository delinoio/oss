// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"strings"
)

type FailureCause string

const ConfigurationNameConflict FailureCause = "configuration_name_conflict"

// ConfigurationNameKey compares display names without changing their spelling,
// internal whitespace, resource identity, or existing text validity limits.
func ConfigurationNameKey(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(strings.TrimSpace(name))))
}

func NameConflict(kind Kind) *Error {
	noun := "project"
	if kind == RepositoryKind {
		noun = "repository"
	}
	return &Error{Code: Conflict, Cause: string(ConfigurationNameConflict), Message: "A " + noun + " with this name already exists.", Guidance: "Choose another name."}
}
