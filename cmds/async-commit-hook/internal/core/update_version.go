package core

import (
	"encoding/json"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Repository release order is publication order, not semantic version order.
// Keep only the maximum while scanning every page; a later page error must not
// turn a partial inventory into an authoritative update or downgrade decision.
func selectUpdateVersion(requested, current string, loadPage func(int) ([]byte, error)) (string, error) {
	if requested != "" {
		if !releaseVersion.MatchString(requested) {
			return "", E("invalid-release-version", "select an exact canonical MAJOR.MINOR.PATCH with --version", 2)
		}
		return requested, nil
	}
	latest := ""
	for page := 1; ; page++ {
		b, err := loadPage(page)
		if err != nil {
			return "", err
		}
		var releases []struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if err = json.Unmarshal(b, &releases); err != nil {
			return "", err
		}
		for _, release := range releases {
			version, found := strings.CutPrefix(release.Tag, "async-commit-hook@v")
			if !found || release.Draft || release.Prerelease || !releaseVersion.MatchString(version) {
				continue
			}
			if latest == "" || semver.Compare("v"+version, "v"+latest) > 0 {
				latest = version
			}
		}
		if len(releases) < 100 {
			break
		}
	}
	if latest == "" {
		return "", E("invalid-release-version", "no supported release found; select an exact MAJOR.MINOR.PATCH with --version", 2)
	}
	if !releaseVersion.MatchString(current) {
		return "", E("invalid-release-version", "installed version is not canonical; select an exact MAJOR.MINOR.PATCH with --version", 2)
	}
	if semver.Compare("v"+latest, "v"+current) < 0 {
		return "", E("update-downgrade-denied", "latest published release is older than this installation; an intentional downgrade requires --version", 2)
	}
	return latest, nil
}
