package messages

import "testing"

func TestFormatDiagnosticTable(t *testing.T) {
	testCases := []struct {
		name     string
		actual   string
		expected string
	}{
		{
			name:     "task not found",
			actual:   FormatDiagnostic(DiagnosticTaskNotFound, "Build"),
			expected: "Task \"Build\" was not found in the current module.",
		},
		{
			name:     "run arg type mismatch",
			actual:   FormatDiagnostic(DiagnosticInvalidRunArgumentType, "target", "string"),
			expected: "Invalid type for run argument \"target\": expected string.",
		},
		{
			name:     "duplicate declaration",
			actual:   FormatDiagnostic(DiagnosticDuplicateTaskDeclaration, "Build"),
			expected: "Task \"Build\" is declared more than once. Remove or rename duplicates.",
		},
		{
			name:     "import cycle",
			actual:   FormatDiagnostic(DiagnosticImportCycle, "pkg/lib"),
			expected: "Import cycle detected while loading \"pkg/lib\".",
		},
		{
			name:     "import not found",
			actual:   FormatDiagnostic(DiagnosticImportResolveFailed, "pkg/lib", "file does not exist"),
			expected: "Import \"pkg/lib\" could not be resolved. file does not exist",
		},
		{
			name:     "parser expectation",
			actual:   FormatDiagnostic(DiagnosticExpectedParameterListClose),
			expected: "Parameter list is not closed. Add ')' after parameters.",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.actual != testCase.expected {
				t.Fatalf("unexpected diagnostic message. expected=%q actual=%q", testCase.expected, testCase.actual)
			}
		})
	}
}

func TestFormatErrorTable(t *testing.T) {
	testCases := []struct {
		name     string
		actual   string
		expected string
	}{
		{
			name:     "entry extension",
			actual:   FormatError(ErrorEntryFileExtension, "./main.txt"),
			expected: "Entry path \"./main.txt\" must use the .ttl extension.",
		},
		{
			name:     "entry escape",
			actual:   FormatError(ErrorEntryEscapesWorkspace, "../outside.ttl"),
			expected: "Entry path \"../outside.ttl\" escapes the workspace root.",
		},
		{
			name:     "out dir escape",
			actual:   FormatError(ErrorOutDirEscapesWorkspace, "../out"),
			expected: "Output directory path \"../out\" escapes the workspace root.",
		},
		{
			name:     "import escape",
			actual:   FormatError(ErrorImportEscapesWorkspace, "../evil.ttl"),
			expected: "Import path \"../evil.ttl\" escapes the workspace root.",
		},
		{
			name:     "cache open",
			actual:   FormatError(ErrorOpenCacheStore, "/tmp/cache.sqlite3"),
			expected: "Could not open the TTL cache store at \"/tmp/cache.sqlite3\".",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.actual != testCase.expected {
				t.Fatalf("unexpected error message. expected=%q actual=%q", testCase.expected, testCase.actual)
			}
		})
	}
}
