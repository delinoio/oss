package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const commitSubjectBytes = 4096
const commitHeaderBytes = 16 << 10
const commitPageSize = 100

func Commits(ctx context.Context, path, ref string, offset int) ([]Commit, error) {
	if offset < 0 || offset > 1000000 {
		return nil, E("invalid-offset", "invalid commit offset", 2)
	}
	sha, err := ResolveCommit(ctx, path, ref)
	if err != nil {
		return nil, err
	}
	cmd := gitCommand(ctx, path, "log", "--format=%H%x09%P%x09%s", "--max-count=100", fmt.Sprintf("--skip=%d", offset), sha, "--")
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	commits, readErr := readCommitPage(output)
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, E("git-error", "cannot read committed history", 3)
	}
	return commits, nil
}

// Drain subject chunks while retaining only a display prefix. A fixed page of
// 100 bounded headers and 4 KiB subjects stays below 5 MiB even with JSON escapes.
// Preserve every parent ID or reject the record, never silently trim ancestry.
func readCommitPage(input io.Reader) ([]Commit, error) {
	reader := bufio.NewReaderSize(input, commitHeaderBytes)
	out := []Commit{}
	for {
		id, err := reader.ReadSlice('\t')
		if errors.Is(err, io.EOF) && len(id) == 0 {
			return out, nil
		}
		if err != nil || !objectID.Match(id[:len(id)-1]) {
			return nil, E("git-log-invalid", "invalid committed history object ID", 3)
		}
		if len(out) == commitPageSize {
			return nil, E("git-log-invalid", "committed history exceeded the page limit", 3)
		}
		commit := Commit{ID: string(id[:len(id)-1])}
		parents, err := reader.ReadSlice('\t')
		if err != nil {
			return nil, E("git-log-invalid", "commit parent header exceeds the supported 16 KiB limit or is incomplete", 3)
		}
		commit.Parents = strings.Fields(string(parents[:len(parents)-1]))
		for _, parent := range commit.Parents {
			if !objectID.MatchString(parent) {
				return nil, E("git-log-invalid", "invalid committed history parent ID", 3)
			}
		}
		subject := make([]byte, 0, commitSubjectBytes+utf8.UTFMax)
		truncated := false
		for {
			chunk, err := reader.ReadSlice('\n')
			if err == nil {
				chunk = chunk[:len(chunk)-1]
			}
			keep := min(len(chunk), cap(subject)-len(subject))
			subject = append(subject, chunk[:keep]...)
			truncated = truncated || keep < len(chunk)
			if err == nil {
				break
			}
			if !errors.Is(err, bufio.ErrBufferFull) {
				return nil, E("git-log-invalid", "incomplete committed history subject", 3)
			}
		}
		clean := strings.ToValidUTF8(string(subject), "\uFFFD")
		if truncated || len(clean) > commitSubjectBytes {
			end := min(len(clean), commitSubjectBytes-len("…"))
			for end > 0 && end < len(clean) && !utf8.RuneStart(clean[end]) {
				end--
			}
			clean = clean[:end] + "…"
		}
		commit.Subject = clean
		out = append(out, commit)
	}
}
