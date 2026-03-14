package worker

import "testing"

func TestExtractPRTrackingID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"github PR URL",
			"Created PR: https://github.com/owner/repo/pull/123",
			"owner/repo#123",
		},
		{
			"github PR URL with trailing text",
			"See https://github.com/acme/project/pull/456 for details",
			"acme/project#456",
		},
		{
			"no PR URL",
			"This is just a regular message",
			"",
		},
		{
			"github non-PR URL",
			"See https://github.com/owner/repo/issues/789",
			"",
		},
		{
			"multiple PR URLs returns first",
			"PR https://github.com/a/b/pull/1 and https://github.com/c/d/pull/2",
			"a/b#1",
		},
		{
			"empty input",
			"",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPRTrackingID(tt.input)
			if got != tt.want {
				t.Fatalf("extractPRTrackingID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSummarizePrompt(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"short prompt", "short prompt"},
		{
			"this is a very long prompt that should be truncated because it exceeds eighty characters in length total here",
			"this is a very long prompt that should be truncated because it exceeds eighty ch",
		},
	}

	for _, tt := range tests {
		got := summarizePrompt(tt.input)
		if got != tt.want {
			t.Fatalf("summarizePrompt(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
