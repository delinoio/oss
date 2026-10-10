// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"strings"
)

const ConfigurationNameConflictCause = "configuration_name_conflict"

// ConfigurationNameKey compares spelling without changing stored display text.
func ConfigurationNameKey(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(strings.TrimSpace(name))))
}

func ConfigurationNameConflict() *Error {
	e := Fail(Conflict, "A configuration name is already in use.", "Choose another name for this resource.")
	e.Cause = ConfigurationNameConflictCause
	return e
}
