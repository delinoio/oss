package domain

import "strings"

type PRHeadRepositoryState string

const (
	PRHeadRepositoryAvailable   PRHeadRepositoryState = "available"
	PRHeadRepositoryUnavailable PRHeadRepositoryState = "unavailable"
)

// A nil observation means historical data did not record this fact. A present
// unavailable observation preserves GitHub's explicit null head repository.
// Neither permits substituting the PR's base repository as a push destination.
type PRHeadRepositoryObservation struct {
	State      PRHeadRepositoryState `json:"state"`
	Repository *RemoteRepository     `json:"repository,omitempty"`
}

func (v PRHeadRepositoryObservation) Validate(base RemoteRepository) error {
	if v.State == PRHeadRepositoryUnavailable && v.Repository == nil {
		return nil
	}
	r := v.Repository
	if v.State != PRHeadRepositoryAvailable || r == nil || r.Provider != GitHubCom || !PositiveDecimal(r.ID) || Text(r.NodeID, "head repository node", 256, true) != nil || ValidateGitHubRepository(r.Owner, r.Name) != nil || Text(r.DefaultBranch, "default branch", 1024, false) != nil || strings.ContainsAny(r.DefaultBranch, "\r\n") {
		return invalidPRGitTarget()
	}
	if r.ID == base.ID {
		if r.NodeID != base.NodeID || !strings.EqualFold(r.Owner, base.Owner) || !strings.EqualFold(r.Name, base.Name) {
			return invalidPRGitTarget()
		}
	} else if r.NodeID == base.NodeID || (strings.EqualFold(r.Owner, base.Owner) && strings.EqualFold(r.Name, base.Name)) {
		return invalidPRGitTarget()
	}
	return nil
}

// PRGitTarget is a non-secret identity snapshot for a future Worker preflight.
// It grants no Git execution by itself. The Worker must verify its prepared
// checkout, selected remote, exact head and native Git authentication anew.
type PRGitTarget struct {
	Version        uint32             `json:"version"`
	Target         SessionPullRequest `json:"target"`
	HeadRepository RemoteRepository   `json:"head_repository"`
	BaseRef        string             `json:"base_ref"`
	BaseSHA        string             `json:"base_sha"`
	HeadRef        string             `json:"head_ref"`
	HeadSHA        string             `json:"head_sha"`
}

func (v PRGitTarget) Validate() error {
	base := RemoteRepository{Provider: v.Target.Provider, ID: v.Target.RemoteRepositoryID, NodeID: v.Target.RepositoryNodeID, Owner: v.Target.Owner, Name: v.Target.Name}
	head := PRHeadRepositoryObservation{State: PRHeadRepositoryAvailable, Repository: &v.HeadRepository}
	if v.Version != 1 || v.Target.Validate() != nil || head.Validate(base) != nil || Text(v.BaseRef, "base ref", 1024, true) != nil || Text(v.HeadRef, "head ref", 1024, true) != nil || strings.ContainsAny(v.BaseRef+v.HeadRef, "\r\n") || !repositorySHA(v.BaseSHA) || !repositorySHA(v.HeadSHA) {
		return invalidPRGitTarget()
	}
	return nil
}

func NewPRGitTarget(observed RepositoryQueryResult) (PRGitTarget, error) {
	var v PRGitTarget
	target, err := PRTargetFromObservation(observed)
	if err != nil {
		return v, err
	}
	item := observed.Items[0]
	if observed.Query.Operation != RepositoryDetail || item.State != RepositoryItemOpen || item.Merged == nil || *item.Merged || item.HeadRepository == nil || item.HeadRepository.State != PRHeadRepositoryAvailable || item.HeadRepository.Repository == nil {
		return v, Fail(Unavailable, "The PR's current source repository is unavailable for Git preparation.", "Refresh the open PR and restore its original source repository; the base repository is never a replacement push destination.")
	}
	v = PRGitTarget{Version: 1, Target: target, HeadRepository: *item.HeadRepository.Repository, BaseRef: item.BaseRef, BaseSHA: item.BaseSHA, HeadRef: item.HeadRef, HeadSHA: item.HeadSHA}
	return v, v.Validate()
}

func invalidPRGitTarget() error {
	return Fail(RecoveryRequired, "The PR source repository identity is inconsistent.", "Refresh the exact original PR; do not infer its Git destination from a branch name or base repository.")
}
