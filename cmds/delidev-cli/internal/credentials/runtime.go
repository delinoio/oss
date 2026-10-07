// SPDX-License-Identifier: Apache-2.0
package credentials

// Closed runtime diagnoses contain no native content or filesystem paths.
const (
	ExecutableChangedCause = "credential_executable_changed"
	ExecutableInvalidCause = "credential_executable_invalid"
)
