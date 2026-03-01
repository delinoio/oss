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

		value := entry.Value
		if strings.Contains(value, "\n") || strings.Contains(value, "\r") {
			delimiter := outputDelimiterFor(value)
			if _, err := fmt.Fprintf(outputFile, "%s<<%s\n%s\n%s\n", key, delimiter, value, delimiter); err != nil {
				return err
			}
			continue
		}

		if _, err := fmt.Fprintf(outputFile, "%s=%s\n", key, value); err != nil {
			return err
		}
	}

	return nil
}

func outputDelimiterFor(value string) string {
	delimiter := "COMMIT_TRACKER_OUTPUT_EOF"
	for strings.Contains(value, delimiter) {
		delimiter += "_X"
	}
	return delimiter
}
