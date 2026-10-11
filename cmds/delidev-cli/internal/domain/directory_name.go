package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// DefaultManagedDirectoryName replaces a registered name that converts to nothing.
const DefaultManagedDirectoryName = "repository"

const maxManagedDirectoryBytes = 255

// PortableDirectoryName converts a registered repository name into one
// deterministic portable folder name. The result always passes
// ValidateRepositoryCloneDirectory. Ordinary valid names keep their casing.
func PortableDirectoryName(name string) string {
	value := norm.NFC.String(strings.TrimSpace(name))
	value = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || c == utf8.RuneError || strings.ContainsRune("<>:\"/\\|?*", c) {
			return '_'
		}
		return c
	}, value)
	value = trimManagedDirectoryName(value)
	if value == "" {
		value = DefaultManagedDirectoryName
	}
	stem := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		value = "_" + value
	}
	value = trimManagedDirectoryName(truncateManagedDirectoryName(value))
	if value == "" || ValidateRepositoryCloneDirectory(value) != nil {
		return DefaultManagedDirectoryName
	}
	return value
}

func trimManagedDirectoryName(value string) string {
	return strings.TrimRightFunc(value, func(c rune) bool { return c == '.' || unicode.IsSpace(c) })
}

func truncateManagedDirectoryName(value string) string {
	if len(value) <= maxManagedDirectoryBytes {
		return value
	}
	cut := maxManagedDirectoryBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}
