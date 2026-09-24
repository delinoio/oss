package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func ResultUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The Worker workspace result does not prove the accepted preparation.", "Preserve its files and reconcile the original job and manifest before retrying.")
}

// ValidateResult checks the remote manifest without interpreting Worker paths on
// the server OS. This is publication validation, not permission to open those
// paths: confinement and native filesystem checks remain the Worker's duty.
func ValidateResult(input PrepareRequest, result Manifest, workerOS string) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return ResultUncertain()
	}
	digest := sha256.Sum256(raw)
	if result.Version != 1 || result.State != Ready || result.SessionID != input.SessionID || result.MachineID != input.MachineID || result.Type != input.Type || result.InputDigest != hex.EncodeToString(digest[:]) || result.CreatedAt.IsZero() || len(result.Repositories) != len(input.Repositories) || len(result.Repositories) > 100 {
		return ResultUncertain()
	}
	normalize := func(value string) string {
		if workerOS == "windows" {
			return strings.ReplaceAll(value, "\\", "/")
		}
		return value
	}
	absolute := func(value string) bool {
		if domain.Text(value, "workspace path", 4096, true) != nil {
			return false
		}
		value = normalize(value)
		if workerOS == "windows" {
			if strings.HasPrefix(value, "//") {
				parts := strings.Split(value[2:], "/")
				if len(parts) < 3 || parts[0] == "" || parts[0] == "." || parts[0] == "?" || parts[1] == "" {
					return false
				}
				value = value[1:]
			} else {
				if len(value) < 4 || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) || value[1:3] != ":/" {
					return false
				}
				value = value[2:]
			}
		} else if workerOS != "darwin" && workerOS != "linux" {
			return false
		}
		return path.IsAbs(value) && path.Clean(value) == value
	}
	if !absolute(result.PrimaryPath) {
		return ResultUncertain()
	}
	primary := normalize(result.PrimaryPath)
	if input.Type == domain.GeneralChat {
		if len(input.Repositories) != 0 || input.PrimaryRepository != "" || !strings.HasSuffix(primary, "/workspaces/"+string(input.SessionID)+"/chat") {
			return ResultUncertain()
		}
		return nil
	}
	if input.Type != domain.Worktree && input.Type != domain.Local {
		return ResultUncertain()
	}
	if len(input.Repositories) == 0 {
		return ResultUncertain()
	}
	seen := make(map[domain.ID]bool)
	primaryFound := false
	ownedRoot := ""
	for i, repo := range result.Repositories {
		expected := input.Repositories[i]
		if repo.ID != expected.ID || repo.ID.Validate() != nil || seen[repo.ID] || !absolute(repo.Source) || !absolute(repo.Path) || repo.Source != expected.Checkout || !canonicalCommit(repo.BaseCommit) || !canonicalCommit(repo.StartingCommit) || repo.Owned != (input.Type == domain.Worktree) {
			return ResultUncertain()
		}
		seen[repo.ID] = true
		if input.Type == domain.Worktree {
			if repo.Starting.Validate(false) != nil || repo.Base.Validate(false) != nil || (expected.Starting.Type != "" && repo.Starting != expected.Starting) || (expected.Base.Type != "" && repo.Base != expected.Base) || (expected.Base.Type == "" && repo.Base != repo.Starting) || (repo.Base == repo.Starting && repo.BaseCommit != repo.StartingCommit) {
				return ResultUncertain()
			}
			for ref, commit := range map[domain.Reference]string{repo.Base: repo.BaseCommit, repo.Starting: repo.StartingCommit} {
				if ref.Type == domain.CommitReference && ref.Name != commit {
					return ResultUncertain()
				}
			}
			location := normalize(repo.Path)
			if repo.Path == repo.Source || !strings.HasSuffix(location, "/workspaces/"+string(input.SessionID)+"/"+string(repo.ID)) {
				return ResultUncertain()
			}
			if ownedRoot != "" && path.Dir(location) != ownedRoot {
				return ResultUncertain()
			}
			ownedRoot = path.Dir(location)
		} else if repo.Path != repo.Source || repo.StartingCommit != repo.BaseCommit || repo.Starting != (domain.Reference{Type: domain.CommitReference, Name: repo.StartingCommit}) {
			return ResultUncertain()
		}
		if repo.ID == input.PrimaryRepository {
			if repo.Path != result.PrimaryPath {
				return ResultUncertain()
			}
			primaryFound = true
		}
	}
	if !primaryFound {
		return ResultUncertain()
	}
	return nil
}

func canonicalCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
