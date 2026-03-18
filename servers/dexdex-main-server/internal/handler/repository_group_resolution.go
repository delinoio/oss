package handler

import (
	"fmt"

	dexdexv1 "github.com/delinoio/oss/protos/dexdex/gen/dexdex/v1"
	"github.com/delinoio/oss/servers/dexdex-main-server/internal/store"
)

const implicitRepositoryGroupBranchRef = "HEAD"

// resolveRepositoryGroupForExecution resolves the execution repository group ID.
//
// Resolution order:
// 1. Explicit repository group by ID
// 2. Repository by the same ID as an implicit single-member group
func resolveRepositoryGroupForExecution(
	s store.Store,
	workspaceID string,
	repositoryGroupID string,
) (*dexdexv1.RepositoryGroup, error) {
	repositoryGroup, groupErr := s.GetRepositoryGroup(workspaceID, repositoryGroupID)
	if groupErr == nil {
		return repositoryGroup, nil
	}

	repository, repositoryErr := s.GetRepository(workspaceID, repositoryGroupID)
	if repositoryErr != nil {
		return nil, fmt.Errorf("repository group or repository not found: workspace=%s id=%s", workspaceID, repositoryGroupID)
	}

	return &dexdexv1.RepositoryGroup{
		RepositoryGroupId: repositoryGroupID,
		WorkspaceId:       workspaceID,
		Members: []*dexdexv1.RepositoryGroupMember{
			{
				RepositoryId: repository.RepositoryId,
				BranchRef:    implicitRepositoryGroupBranchRef,
				DisplayOrder: 0,
				Repository:   repository,
			},
		},
	}, nil
}
