package core

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The upstream Git LFS blob decoder considers only blobs below this cutoff.
// A header alone is ordinary source; required pointer fields must also parse.
const lfsPointerCutoff = 1024

var lfsOID = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var lfsExtension = regexp.MustCompile(`^ext-([0-9])-([A-Za-z0-9_][A-Za-z0-9_.-]*)$`)

func isLFSPointer(data []byte) bool {
	if len(data) == 0 || len(data) >= lfsPointerCutoff || !utf8.Valid(data) {
		return false
	}
	required := []string{"version", "oid", "size"}
	index := 0
	priorities := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok || index >= len(required) {
			return false
		}
		if key != required[index] {
			ext := lfsExtension.FindStringSubmatch(key)
			if index == 0 || ext == nil || priorities[ext[1]] || !lfsOID.MatchString(value) {
				return false
			}
			priorities[ext[1]] = true
			continue
		}
		switch key {
		case "version":
			if value != "https://git-lfs.github.com/spec/v1" && value != "https://hawser.github.com/spec/v1" && value != "http://git-media.io/v/2" {
				return false
			}
		case "oid":
			if !lfsOID.MatchString(value) {
				return false
			}
		case "size":
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				return false
			}
		}
		index++
	}
	return index == len(required)
}
