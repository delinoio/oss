package domain

import "testing"

func TestPRGitTargetPreservesForkIdentityWithoutBaseFallback(t *testing.T) {
	observed := conflictObservationFixture()
	if _, err := NewPRGitTarget(observed); err == nil {
		t.Fatal("historical missing head authorized Git")
	}
	observed.Items[0].HeadRepository = &PRHeadRepositoryObservation{State: PRHeadRepositoryUnavailable}
	if observed.Validate() != nil {
		t.Fatal("deleted fork cannot be read")
	}
	if _, err := NewPRGitTarget(observed); err == nil {
		t.Fatal("deleted fork replaced by base repository")
	}
	fork := RemoteRepository{Provider: GitHubCom, ID: "9007199254740993", NodeID: "FORK_1", Owner: "fixture-author", Name: "fork-repo", Private: true, DefaultBranch: "main"}
	observed.Items[0].HeadRepository = &PRHeadRepositoryObservation{State: PRHeadRepositoryAvailable, Repository: &fork}
	value, err := NewPRGitTarget(observed)
	if err != nil || value.HeadRepository.ID != fork.ID || value.HeadSHA != observed.Items[0].HeadSHA || value.Target.RemoteRepositoryID == fork.ID {
		t.Fatal("fork identity", err)
	}
	fork.ID = "17"
	if value.HeadRepository.ID != "9007199254740993" {
		t.Fatal("target aliased later observation")
	}
	observed.Items[0].HeadRepository.Repository = &observed.Repository
	if _, err = NewPRGitTarget(observed); err != nil {
		t.Fatal("same repository PR", err)
	}
}

func TestPRHeadRepositoryObservationRejectsContradictoryNamespaces(t *testing.T) {
	for _, scenario := range []string{"id", "node", "name", "state", "missing", "foreign-provider", "closed", "list", "issue"} {
		t.Run(scenario, func(t *testing.T) {
			o := conflictObservationFixture()
			repo := o.Repository
			o.Items[0].HeadRepository = &PRHeadRepositoryObservation{State: PRHeadRepositoryAvailable, Repository: &repo}
			switch scenario {
			case "id":
				repo.ID = "99"
			case "node":
				repo.NodeID = "OTHER"
			case "name":
				repo.Name = "different"
			case "state":
				o.Items[0].HeadRepository.State = PRHeadRepositoryUnavailable
			case "missing":
				o.Items[0].HeadRepository.Repository = nil
			case "foreign-provider":
				repo.Provider = "other"
			case "closed":
				o.Items[0].State = RepositoryItemClosed
			case "list":
				o.Query.Operation = RepositoryList
				o.Query.Number = ""
			case "issue":
				o.Items[0].Kind = RepositoryIssue
			}
			if _, err := NewPRGitTarget(o); err == nil {
				t.Fatal("contradictory Git target accepted")
			}
		})
	}
}
