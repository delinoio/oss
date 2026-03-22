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
			actual:   FormatDiagnostic(DiagnosticInvalidRunArgumentType, "target", "target", "string", "integer"),
			expected: "Invalid run argument \"target\" at \"target\". Expected string, got integer.",
		},
		{
			name:     "duplicate declaration",
			actual:   FormatDiagnostic(DiagnosticDuplicateTaskDeclaration, "Build"),
			expected: "Duplicate task declaration \"Build\". Remove or rename one declaration.",
		},
		{
			name:     "import cycle",
			actual:   FormatDiagnostic(DiagnosticImportCycle, "pkg/lib"),
			expected: "Import cycle detected while loading \"pkg/lib\".",
		},
		{
			name:     "import not found",
			actual:   FormatDiagnostic(DiagnosticImportResolveFailed, "pkg/lib", "file does not exist"),
			expected: "Failed to resolve import \"pkg/lib\". Cause: file does not exist.",
		},
		{
			name:     "parser expectation",
			actual:   FormatDiagnostic(DiagnosticExpectedParameterListClose),
			expected: "Invalid parameter list. Expected ')' to close parameters.",
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
			actual:   FormatError(ErrorEntryFileExtension, "./main.txt", ".txt"),
			expected: "Entry path \"./main.txt\" must use the .ttl extension (detected extension \".txt\").",
		},
		{
			name:     "entry escape",
			actual:   FormatError(ErrorEntryEscapesWorkspace, "../outside.ttl", "/tmp/workspace"),
			expected: "Entry path \"../outside.ttl\" escapes workspace root \"/tmp/workspace\".",
		},
		{
			name:     "out dir escape",
			actual:   FormatError(ErrorOutDirEscapesWorkspace, "../out", "/tmp/workspace"),
			expected: "Output directory \"../out\" escapes workspace root \"/tmp/workspace\".",
		},
		{
			name:     "import escape",
			actual:   FormatError(ErrorImportEscapesWorkspace, "../evil.ttl", "/tmp/workspace"),
			expected: "Import path \"../evil.ttl\" escapes workspace root \"/tmp/workspace\".",
		},
		{
			name:     "cache open",
			actual:   FormatError(ErrorOpenCacheStore, "/tmp/cache.sqlite3"),
			expected: "Failed to open TTL cache store at \"/tmp/cache.sqlite3\".",
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
