package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type githubOutputEntry struct {
	Key   string
	Value string
}

func resolveGitHubOutputPath(explicitPath string) string {
	trimmed := strings.TrimSpace(explicitPath)
	if trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(os.Getenv("GITHUB_OUTPUT"))
}

func appendGitHubOutput(path string, entries []githubOutputEntry) error {
	cleanedPath := strings.TrimSpace(path)
	if cleanedPath == "" || len(entries) == 0 {
		return nil
	}

	outputFile, err := os.OpenFile(filepath.Clean(cleanedPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer outputFile.Close()

	for _, entry := range entries {
		key := strings.TrimSpace(entry.Key)
		if key == "" {
			continue
		}
		if _, err := fmt.Fprintf(outputFile, "%s=%s\n", key, escapeGitHubOutputValue(entry.Value)); err != nil {
			return err
		}
	}

	return nil
}

func escapeGitHubOutputValue(value string) string {
	escaped := strings.ReplaceAll(value, "%", "%25")
	escaped = strings.ReplaceAll(escaped, "\r", "%0D")
	escaped = strings.ReplaceAll(escaped, "\n", "%0A")
	return escaped
}
