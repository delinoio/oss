package workspace

import (
	"context"
	"strings"
)

// Git emits these closed rev-parse queries in argument order. Require the exact
// newline-delimited shape so a path containing a newline cannot shift fields or
// supply another administrative identity. Do not trim meaningful path spaces.
func parseGitFields(raw []byte, count int) ([]string, error) {
	lines := strings.Split(string(raw), "\n")
	if len(lines) != count+1 || lines[count] != "" {
		return nil, ResultUncertain()
	}
	lines = lines[:count]
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.ContainsAny(line, "\r\x00") {
			return nil, ResultUncertain()
		}
		lines[i] = line
	}
	return lines, nil
}

func (g Git) revParseFields(ctx context.Context, root string, count int, options ...string) ([]string, error) {
	args := append([]string{"rev-parse", "--path-format=absolute"}, options...)
	raw, err := g.run(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	return parseGitFields(raw, count)
}
