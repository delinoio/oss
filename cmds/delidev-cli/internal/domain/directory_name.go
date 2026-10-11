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
	value := norm.NFC.String(strings.TrimSpace(strings.ToValidUTF8(name, "_")))
	value = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) || strings.ContainsRune("<>:\"/\\|?*", c) {
			return '_'
		}
		return c
	}, value)
	value = trimManagedDirectoryName(truncateManagedDirectoryName(trimManagedDirectoryName(value)))
	if value == "" {
		value = DefaultManagedDirectoryName
	}
	if reservedDeviceName(value) || strings.EqualFold(value, ".git") {
		value = trimManagedDirectoryName(truncateManagedDirectoryName("_" + value))
	}
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

// reservedDeviceName reports Windows device names, including the superscript
// digit forms that Windows also reserves.
func reservedDeviceName(value string) bool {
	stem := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT") {
		switch strings.TrimPrefix(strings.TrimPrefix(stem, "COM"), "LPT") {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "\u00b9", "\u00b2", "\u00b3":
			return true
		}
	}
	return false
}
