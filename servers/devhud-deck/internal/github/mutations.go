package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const (
	maxMutationOperands = 100
	maxGitHubAssignees  = 10
)

func (client *Client) Mutate(
	ctx context.Context,
	actor string,
	installationID uint64,
	credential Credential,
	appPermissions Permissions,
	reference PullRequestRef,
	expectedRevision uint64,
	mutation Mutation,
) (MutationResult, error) {
	if actor == "" || expectedRevision == 0 ||
		mutation.Kind <= MutationUnknown ||
		mutation.Kind > MutationCancelAutoMerge {
		return MutationResult{}, ErrUnsupportedAction
	}
	if !client.mutations.allow(actor, client.now()) {
		return MutationResult{}, ErrMutationRateLimited
	}
	metadata, err := client.ActionMetadata(
		ctx, installationID, credential, appPermissions, reference)
	if err != nil {
		return MutationResult{}, err
	}
	if metadata.Revision != expectedRevision {
		return MutationResult{}, ErrStaleRevision
	}
	if mutation.Kind == MutationMerge && !mutation.Confirmed {
		return MutationResult{}, ErrConfirmationRequired
	}
	if !metadata.Supported[mutation.Kind] {
		if mutation.Kind == MutationMerge {
			if metadata.MergeConflicting {
				return MutationResult{}, ErrStaleRevision
			}
			if metadata.MergeBlocked {
				return MutationResult{}, ErrBranchProtected
			}
		}
		return MutationResult{}, ErrPermissionDenied
	}
	if mutation.Kind == MutationMerge || mutation.Kind == MutationEnableAutoMerge {
		if !metadata.AvailableMethods[mutation.MergeMethod] {
			return MutationResult{}, ErrBranchProtected
		}
	}
	if err := validateMutation(reference, mutation); err != nil {
		return MutationResult{}, err
	}
	release, err := client.concurrency.acquire(installationID)
	if err != nil {
		return MutationResult{}, err
	}
	defer release()
	err = client.applyMutation(ctx, credential, reference, metadata, mutation)
	if err != nil {
		return MutationResult{}, err
	}
	current, err := client.actionMetadata(
		ctx, credential, appPermissions, reference)
	if err != nil {
		return MutationResult{
			Kind: mutation.Kind, RefreshRequired: true,
		}, nil
	}
	// ActionMetadata does not contain review-decision or check-summary state.
	// Require the client to refresh rather than combine it with a stale snapshot.
	return MutationResult{
		Kind: mutation.Kind, Revision: current.Revision, Metadata: current,
		RefreshRequired: true,
	}, nil
}

func validateMutation(reference PullRequestRef, mutation Mutation) error {
	if err := reference.Validate(); err != nil {
		return err
	}
	switch mutation.Kind {
	case MutationAssignUsers, MutationUnassignUsers:
		return validateUsers(mutation.Users, maxGitHubAssignees)
	case MutationRequestReviewers, MutationRemoveReviewers:
		if len(mutation.Users) == 0 && len(mutation.Teams) == 0 {
			return ErrUnsupportedAction
		}
		if len(mutation.Users)+len(mutation.Teams) > maxMutationOperands {
			return ErrUnsupportedAction
		}
		if len(mutation.Users) > 0 {
			if err := validateUsers(
				mutation.Users, maxMutationOperands); err != nil {
				return err
			}
		}
		for _, team := range mutation.Teams {
			if !safePathSegment(team.Organization) || !safePathSegment(team.Slug) ||
				!strings.EqualFold(team.Organization, reference.Repository.Owner) {
				return ErrPermissionDenied
			}
		}
	case MutationAddLabels, MutationRemoveLabels:
		if len(mutation.Labels) == 0 ||
			len(mutation.Labels) > maxMutationOperands {
			return ErrUnsupportedAction
		}
		for _, label := range mutation.Labels {
			if !validOperand(label) {
				return ErrUnsupportedAction
			}
		}
	case MutationMerge, MutationEnableAutoMerge:
		if mutation.MergeMethod < MergeMethodMerge ||
			mutation.MergeMethod > MergeMethodRebase {
			return ErrUnsupportedAction
		}
	case MutationMarkDraft, MutationMarkReady, MutationClose, MutationReopen,
		MutationCancelAutoMerge:
		// These mutations intentionally have no caller-supplied operands.
	default:
		return ErrUnsupportedAction
	}
	return nil
}

func validateUsers(users []User, limit int) error {
	if len(users) == 0 || len(users) > limit {
		return ErrUnsupportedAction
	}
	for _, user := range users {
		if !safePathSegment(user.Login) {
			return ErrUnsupportedAction
		}
	}
	return nil
}

func validOperand(value string) bool {
	return value != "" && len(value) <= 255 &&
		strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func (client *Client) applyMutation(
	ctx context.Context,
	credential Credential,
	reference PullRequestRef,
	metadata ActionMetadata,
	mutation Mutation,
) error {
	pullSuffix := fmt.Sprintf("/pulls/%d", reference.Number)
	issueSuffix := fmt.Sprintf("/issues/%d", reference.Number)
	switch mutation.Kind {
	case MutationAssignUsers, MutationUnassignUsers:
		path, _ := repositoryPath(reference.Repository, issueSuffix+"/assignees")
		method := http.MethodPost
		if mutation.Kind == MutationUnassignUsers {
			method = http.MethodDelete
		}
		_, err := client.do(ctx, credential, method, path,
			map[string]any{"assignees": userLogins(mutation.Users)}, nil)
		return err
	case MutationRequestReviewers, MutationRemoveReviewers:
		path, _ := repositoryPath(reference.Repository,
			pullSuffix+"/requested_reviewers")
		method := http.MethodPost
		if mutation.Kind == MutationRemoveReviewers {
			method = http.MethodDelete
		}
		_, err := client.do(ctx, credential, method, path, map[string]any{
			"reviewers":      userLogins(mutation.Users),
			"team_reviewers": teamSlugs(mutation.Teams),
		}, nil)
		return err
	case MutationAddLabels:
		path, _ := repositoryPath(reference.Repository, issueSuffix+"/labels")
		_, err := client.do(ctx, credential, http.MethodPost, path,
			map[string]any{"labels": mutation.Labels}, nil)
		return err
	case MutationRemoveLabels:
		labelIDs := make([]string, 0, len(mutation.Labels))
		for _, label := range mutation.Labels {
			labelID := metadata.LabelIDs[strings.ToLower(label)]
			if labelID == "" {
				return ErrStaleRevision
			}
			labelIDs = append(labelIDs, labelID)
		}
		return client.graphQL(ctx, credential, `
mutation($id:ID!,$labels:[ID!]!){
  removeLabelsFromLabelable(input:{labelableId:$id,labelIds:$labels}){
    labelable{... on PullRequest{id}}
  }
}`, map[string]any{"id": metadata.NodeID, "labels": labelIDs})
	case MutationMarkDraft:
		return client.graphQL(ctx, credential, `
mutation($id:ID!){convertPullRequestToDraft(input:{pullRequestId:$id}){
  pullRequest{id}
}}`, map[string]any{"id": metadata.NodeID})
	case MutationMarkReady:
		return client.graphQL(ctx, credential, `
mutation($id:ID!){markPullRequestReadyForReview(input:{pullRequestId:$id}){
  pullRequest{id}
}}`, map[string]any{"id": metadata.NodeID})
	case MutationClose, MutationReopen:
		state := "closed"
		if mutation.Kind == MutationReopen {
			state = "open"
		}
		path, _ := repositoryPath(reference.Repository, pullSuffix)
		_, err := client.do(ctx, credential, http.MethodPatch, path,
			map[string]any{"state": state}, nil)
		return err
	case MutationMerge:
		path, _ := repositoryPath(reference.Repository, pullSuffix+"/merge")
		_, err := client.doWithConflictError(
			ctx, credential, http.MethodPut, path,
			map[string]any{
				"sha":          metadata.HeadSHA,
				"merge_method": mergeMethodREST(mutation.MergeMethod),
			}, nil, ErrStaleRevision)
		return err
	case MutationEnableAutoMerge:
		return client.graphQL(ctx, credential, `
mutation($id:ID!,$method:PullRequestMergeMethod!,$head:GitObjectID!){
  enablePullRequestAutoMerge(input:{
    pullRequestId:$id,mergeMethod:$method,expectedHeadOid:$head
  }){
    pullRequest{id}
  }
}`, map[string]any{
			"id": metadata.NodeID, "method": mergeMethodGraphQL(mutation.MergeMethod),
			"head": metadata.HeadSHA,
		})
	case MutationCancelAutoMerge:
		return client.graphQL(ctx, credential, `
mutation($id:ID!){disablePullRequestAutoMerge(input:{pullRequestId:$id}){
  pullRequest{id}
}}`, map[string]any{"id": metadata.NodeID})
	default:
		return ErrUnsupportedAction
	}
}

func userLogins(users []User) []string {
	values := make([]string, len(users))
	for index, user := range users {
		values[index] = user.Login
	}
	return values
}

func teamSlugs(teams []Team) []string {
	values := make([]string, len(teams))
	for index, team := range teams {
		values[index] = team.Slug
	}
	return values
}

func mergeMethodREST(method MergeMethod) string {
	switch method {
	case MergeMethodMerge:
		return "merge"
	case MergeMethodSquash:
		return "squash"
	case MergeMethodRebase:
		return "rebase"
	default:
		return ""
	}
}

func mergeMethodGraphQL(method MergeMethod) string {
	return strings.ToUpper(mergeMethodREST(method))
}

func (client *Client) graphQL(
	ctx context.Context,
	credential Credential,
	query string,
	variables map[string]any,
) error {
	var response struct {
		Errors []struct {
			Type string `json:"type"`
		} `json:"errors"`
	}
	headers, err := client.do(ctx, credential, http.MethodPost, GraphQLPath,
		map[string]any{"query": query, "variables": variables}, &response)
	if err != nil {
		return err
	}
	if len(response.Errors) == 0 {
		return nil
	}
	if providerRateLimited(headers) {
		return &RateLimitError{RetryAfter: retryDuration(headers, client.now())}
	}
	for _, failure := range response.Errors {
		switch strings.ToUpper(failure.Type) {
		case "FORBIDDEN", "NOT_FOUND":
			return ErrPermissionDenied
		case "UNPROCESSABLE", "CONFLICT":
			return ErrBranchProtected
		}
	}
	return ErrProvider
}
