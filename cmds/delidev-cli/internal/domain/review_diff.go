package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

type ReviewSide string
type ReviewAnchorKind string
type ReviewFileKind string

const (
	ReviewOldSide     ReviewSide       = "old"
	ReviewNewSide     ReviewSide       = "new"
	ReviewFileAnchor  ReviewAnchorKind = "file"
	ReviewLineAnchor  ReviewAnchorKind = "lines"
	ReviewTextFile    ReviewFileKind   = "text"
	ReviewNonLineFile ReviewFileKind   = "non-line"
)

// ReviewLine numbers come only from validated ordinary Git hunks. Zero means
// that this line does not exist on that side; it is never a selectable line.
type ReviewLine struct {
	Old     uint32 `json:"old,omitempty"`
	New     uint32 `json:"new,omitempty"`
	Text    string `json:"text"`
	Newline bool   `json:"newline"`
	Hunk    uint32 `json:"hunk"`
}

type ReviewFile struct {
	Path   string         `json:"path"`
	Kind   ReviewFileKind `json:"kind"`
	Digest string         `json:"digest"`
	Lines  []ReviewLine   `json:"lines"`
}

const MaxReviewContextBytes = 1 << 20

type ReviewContext struct {
	Diff  WorkspaceDiff `json:"diff"`
	Files []ReviewFile  `json:"files"`
}

var reviewHunk = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(?: .*)?$`)
var reviewIndex = regexp.MustCompile(`^index [0-9a-f]{4,64}\.\.[0-9a-f]{4,64}(?: ([0-7]{6}))?$`)

func reviewUnavailable() error {
	return Fail(Unsupported, "This diff cannot provide unambiguous review anchors.", "Refresh or select a narrower ordinary Git comparison; combined, incomplete and unsupported paths cannot supply line locations.")
}

func reviewGitPath(raw, prefix string) (string, bool) {
	if strings.HasPrefix(raw, `"`) {
		v, err := strconv.Unquote(raw)
		if err != nil {
			return "", false
		}
		raw = v
	}
	path, ok := strings.CutPrefix(raw, prefix)
	return path, ok && path != "." && WorkspacePath(path)
}

func reviewHeader(raw string) (string, bool) {
	raw, ok := strings.CutPrefix(raw, "diff --git ")
	if !ok {
		return "", false
	}
	var left, right string
	if strings.HasPrefix(raw, `"`) {
		first, err := strconv.QuotedPrefix(raw)
		if err != nil {
			return "", false
		}
		left = first
		right, ok = strings.CutPrefix(raw[len(first):], " ")
		if !ok {
			return "", false
		}
	} else {
		// --no-renames uses the same path twice. Length and exact equality
		// disambiguate spaces, including a literal " b/" inside a filename.
		if len(raw) < 7 || (len(raw)-5)%2 != 0 {
			return "", false
		}
		n := (len(raw) - 5) / 2
		left, right = raw[:n+2], raw[n+3:]
		if raw[n+2] != ' ' {
			return "", false
		}
	}
	a, aOK := reviewGitPath(left, "a/")
	b, bOK := reviewGitPath(right, "b/")
	return a, aOK && bOK && a == b
}

func reviewMode(raw string) bool {
	return raw == "100644" || raw == "100755" || raw == "120000" || raw == "160000"
}

// ReviewFiles parses only the bounded --no-renames, ordinary unified patch
// produced by the workspace reader. Unknown/ambiguous forms never get guessed
// anchors. Non-line changes retain a file anchor, without synthetic lines.
func (v WorkspaceDiff) ReviewFiles() ([]ReviewFile, error) {
	q := WorkspaceReadQuery{Operation: WorkspaceGitDiff, RepositoryID: v.RepositoryID, Path: v.Path, Comparison: v.Comparison}
	if err := v.Validate(q); err != nil {
		return nil, err
	}
	files := []ReviewFile{}
	if v.Patch == "" {
		return files, nil
	}
	if !strings.HasSuffix(v.Patch, "\n") {
		return nil, reviewUnavailable()
	}
	lines := strings.Split(strings.TrimSuffix(v.Patch, "\n"), "\n")
	seen := map[string]bool{}
	for start := 0; start < len(lines); {
		end := start + 1
		for end < len(lines) && !strings.HasPrefix(lines[end], "diff --git ") {
			end++
		}
		path, ok := reviewHeader(lines[start])
		if !ok || seen[path] || len(files) >= 256 || (v.Path != "." && path != v.Path && !strings.HasPrefix(path, v.Path+"/")) {
			return nil, reviewUnavailable()
		}
		seen[path] = true
		file, err := parseReviewFile(path, lines[start:end])
		if err != nil {
			return nil, err
		}
		files = append(files, file)
		start = end
	}
	return files, nil
}

func parseReviewFile(path string, lines []string) (ReviewFile, error) {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n") + "\n"))
	file := ReviewFile{Path: path, Kind: ReviewNonLineFile, Digest: hex.EncodeToString(sum[:]), Lines: []ReviewLine{}}
	bad := func() (ReviewFile, error) { return ReviewFile{}, reviewUnavailable() }
	oldMode, newMode, index := "", "", false
	added, deleted, binary, headers := false, false, false, false
	i := 1
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "--- ") {
			break
		}
		switch {
		case strings.HasPrefix(line, "index "):
			match := reviewIndex.FindStringSubmatch(line)
			if index || match == nil {
				return bad()
			}
			index = true
			if match[1] != "" {
				if !reviewMode(match[1]) || oldMode != "" || newMode != "" {
					return bad()
				}
				oldMode, newMode = match[1], match[1]
			}
		case strings.HasPrefix(line, "old mode "):
			if oldMode != "" {
				return bad()
			}
			oldMode = strings.TrimPrefix(line, "old mode ")
			if !reviewMode(oldMode) {
				return bad()
			}
		case strings.HasPrefix(line, "new mode "):
			if newMode != "" {
				return bad()
			}
			newMode = strings.TrimPrefix(line, "new mode ")
			if !reviewMode(newMode) {
				return bad()
			}
		case strings.HasPrefix(line, "new file mode "):
			if oldMode != "" || newMode != "" {
				return bad()
			}
			added = true
			oldMode = "000000"
			newMode = strings.TrimPrefix(line, "new file mode ")
			if !reviewMode(newMode) {
				return bad()
			}
		case strings.HasPrefix(line, "deleted file mode "):
			if oldMode != "" || newMode != "" {
				return bad()
			}
			deleted = true
			newMode = "000000"
			oldMode = strings.TrimPrefix(line, "deleted file mode ")
			if !reviewMode(oldMode) {
				return bad()
			}
		case strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ"):
			if binary || !index || i != len(lines)-1 {
				return bad()
			}
			binary = true
		default:
			return bad()
		}
		i++
	}
	if oldMode == "" || newMode == "" {
		return bad()
	}
	if i < len(lines) {
		if binary || !index || i+2 >= len(lines) {
			return bad()
		}
		old, new := strings.TrimSuffix(strings.TrimPrefix(lines[i], "--- "), "\t"), strings.TrimSuffix(strings.TrimPrefix(lines[i+1], "+++ "), "\t")
		if !strings.HasPrefix(lines[i+1], "+++ ") {
			return bad()
		}
		a, aOK := reviewGitPath(old, "a/")
		b, bOK := reviewGitPath(new, "b/")
		if (added && old != "/dev/null") || (!added && (!aOK || a != path)) || (deleted && new != "/dev/null") || (!deleted && (!bOK || b != path)) {
			return bad()
		}
		headers = true
		i += 2
	}
	var hunk, previousOld, previousNew uint32
	oldEOF, newEOF := false, false
	for i < len(lines) {
		match := reviewHunk.FindStringSubmatch(lines[i])
		if match == nil {
			return bad()
		}
		values := [4]uint32{}
		for n := range values {
			raw := match[n+1]
			if raw == "" {
				raw = "1"
			}
			number, err := strconv.ParseUint(raw, 10, 32)
			if err != nil || strconv.FormatUint(number, 10) != raw {
				return bad()
			}
			values[n] = uint32(number)
		}
		o, oc, n, nc := values[0], values[1], values[2], values[3]
		if (oc > 0 && (o == 0 || added || oldEOF)) || (nc > 0 && (n == 0 || deleted || newEOF)) || uint64(o)+uint64(oc) > 1<<32-1 || uint64(n)+uint64(nc) > 1<<32-1 || (oc == 0 && nc == 0) || (hunk > 0 && (o < previousOld || n < previousNew)) {
			return bad()
		}
		previousOld, previousNew = o+oc, n+nc
		hunk++
		i++
		for oc > 0 || nc > 0 {
			if i >= len(lines) || lines[i] == "" {
				return bad()
			}
			line := ReviewLine{Text: lines[i][1:], Newline: true, Hunk: hunk}
			switch lines[i][0] {
			case ' ':
				if oc == 0 || nc == 0 || oldEOF || newEOF {
					return bad()
				}
				line.Old, line.New = o, n
				o++
				n++
				oc--
				nc--
			case '-':
				if oc == 0 || oldEOF {
					return bad()
				}
				line.Old = o
				o++
				oc--
			case '+':
				if nc == 0 || newEOF {
					return bad()
				}
				line.New = n
				n++
				nc--
			default:
				return bad()
			}
			i++
			if i < len(lines) && lines[i] == `\ No newline at end of file` {
				line.Newline = false
				i++
				oldEOF = oldEOF || line.Old != 0
				newEOF = newEOF || line.New != 0
			}
			file.Lines = append(file.Lines, line)
		}
	}
	if headers && hunk == 0 {
		return bad()
	}
	textMode := func(mode string) bool { return mode == "100644" || mode == "100755" || mode == "000000" }
	if len(file.Lines) > 0 && textMode(oldMode) && textMode(newMode) {
		file.Kind = ReviewTextFile
	} else {
		file.Lines = []ReviewLine{}
	}
	return file, nil
}
