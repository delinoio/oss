package domain

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// ConfigurationNameConflictCause identifies a definitive, editable name collision.
const ConfigurationNameConflictCause = "configuration_name_conflict"

// ConfigurationNameKey preserves internal whitespace and never replaces display text.
func ConfigurationNameKey(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(strings.TrimSpace(name))))
}

func ConfigurationNameConflict(kind Kind) *Error {
	noun := "project"
	if kind == RepositoryKind {
		noun = "repository"
	}
	return &Error{Code: Conflict, Cause: ConfigurationNameConflictCause,
		Message:  "A " + noun + " with this name already exists.",
		Guidance: "Choose another name."}
}
