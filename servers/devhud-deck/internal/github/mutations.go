package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
		return MutationResult{}, &RateLimitError{RetryAfter: timeUntilWindow}
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
		if mutation.Kind == MutationMerge && metadata.MergeBlocked {
			return MutationResult{}, ErrBranchProtected
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
	err = client.applyMutation(ctx, credential, reference, metadata, mutation)
	release()
	if err != nil {
		return MutationResult{}, err
	}
	current, err := client.ActionMetadata(
		ctx, installationID, credential, appPermissions, reference)
	if err != nil {
		return MutationResult{}, err
	}
	return MutationResult{
		Kind: mutation.Kind, Revision: current.Revision, Metadata: current,
	}, nil
}

const timeUntilWindow = 60_000_000_000 // one minute in nanoseconds

func validateMutation(reference PullRequestRef, mutation Mutation) error {
	if err := reference.Validate(); err != nil {
		return err
	}
	switch mutation.Kind {
	case MutationAssignUsers, MutationUnassignUsers:
		return validateUsers(mutation.Users)
	case MutationRequestReviewers, MutationRemoveReviewers:
		if len(mutation.Users) == 0 && len(mutation.Teams) == 0 {
			return ErrUnsupportedAction
		}
		if len(mutation.Users) > 0 {
			if err := validateUsers(mutation.Users); err != nil {
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
		if len(mutation.Labels) == 0 {
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

func validateUsers(users []User) error {
	if len(users) == 0 {
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
		for _, label := range mutation.Labels {
			path, _ := repositoryPath(reference.Repository,
				issueSuffix+"/labels/"+url.PathEscape(label))
			if _, err := client.do(
				ctx, credential, http.MethodDelete, path, nil, nil); err != nil {
				return err
			}
		}
		return nil
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
		_, err := client.do(ctx, credential, http.MethodPut, path,
			map[string]any{
				"sha":          metadata.HeadSHA,
				"merge_method": mergeMethodREST(mutation.MergeMethod),
			}, nil)
		return err
	case MutationEnableAutoMerge:
		return client.graphQL(ctx, credential, `
mutation($id:ID!,$method:PullRequestMergeMethod!){
  enablePullRequestAutoMerge(input:{pullRequestId:$id,mergeMethod:$method}){
    pullRequest{id}
  }
}`, map[string]any{
			"id": metadata.NodeID, "method": mergeMethodGraphQL(mutation.MergeMethod),
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
	if _, err := client.do(ctx, credential, http.MethodPost, GraphQLPath,
		map[string]any{"query": query, "variables": variables}, &response); err != nil {
		return err
	}
	if len(response.Errors) == 0 {
		return nil
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
