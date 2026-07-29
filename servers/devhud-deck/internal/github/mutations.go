package github

import (
	"context"
	"encoding/json"
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
	if err == nil {
		current, err = client.completeMutationMetadata(ctx, credential, current)
	}
	if err != nil {
		return MutationResult{
			Kind: mutation.Kind, RefreshRequired: true,
		}, nil
	}
	return MutationResult{
		Kind: mutation.Kind, Revision: current.Revision, Metadata: current,
	}, nil
}

func (client *Client) completeMutationMetadata(
	ctx context.Context,
	credential Credential,
	metadata ActionMetadata,
) (ActionMetadata, error) {
	type stateCount struct {
		State string `json:"state"`
		Count uint32 `json:"count"`
	}
	var data struct {
		Node *struct {
			ReviewDecision    string `json:"reviewDecision"`
			StatusCheckRollup *struct {
				State    string `json:"state"`
				Contexts struct {
					TotalCount                 uint32       `json:"totalCount"`
					CheckRunCountsByState      []stateCount `json:"checkRunCountsByState"`
					StatusContextCountsByState []stateCount `json:"statusContextCountsByState"`
				} `json:"contexts"`
			} `json:"statusCheckRollup"`
		} `json:"node"`
	}
	err := client.graphQLData(ctx, credential, `
query($id:ID!){
  node(id:$id){
    ... on PullRequest{
      reviewDecision
      statusCheckRollup{
        state
        contexts{
          totalCount
          checkRunCountsByState{state count}
          statusContextCountsByState{state count}
        }
      }
    }
  }
}`, map[string]any{"id": metadata.NodeID}, &data)
	if err != nil || data.Node == nil {
		return ActionMetadata{}, ErrProvider
	}
	switch strings.ToUpper(data.Node.ReviewDecision) {
	case "":
		metadata.ReviewDecision = ReviewDecisionUnknown
	case "REVIEW_REQUIRED":
		metadata.ReviewDecision = ReviewDecisionRequired
	case "CHANGES_REQUESTED":
		metadata.ReviewDecision = ReviewDecisionChangesRequested
	case "APPROVED":
		metadata.ReviewDecision = ReviewDecisionApproved
	default:
		return ActionMetadata{}, ErrProvider
	}
	rollup := data.Node.StatusCheckRollup
	if rollup == nil {
		metadata.ChecksState = ChecksStateUnknown
		return metadata, nil
	}
	metadata.PendingChecks = 0
	metadata.SuccessfulChecks = 0
	metadata.FailedChecks = 0
	counts := append(
		append([]stateCount(nil), rollup.Contexts.CheckRunCountsByState...),
		rollup.Contexts.StatusContextCountsByState...)
	for _, count := range counts {
		var target *uint32
		switch strings.ToUpper(count.State) {
		case "EXPECTED", "IN_PROGRESS", "PENDING", "QUEUED", "REQUESTED", "WAITING":
			target = &metadata.PendingChecks
		case "COMPLETED", "NEUTRAL", "SKIPPED", "SUCCESS":
			target = &metadata.SuccessfulChecks
		case "ACTION_REQUIRED", "CANCELLED", "ERROR", "FAILURE", "STALE",
			"STARTUP_FAILURE", "TIMED_OUT":
			target = &metadata.FailedChecks
		default:
			return ActionMetadata{}, ErrProvider
		}
		if count.Count > ^uint32(0)-*target {
			return ActionMetadata{}, ErrProvider
		}
		*target += count.Count
	}
	counted := uint64(metadata.PendingChecks) +
		uint64(metadata.SuccessfulChecks) + uint64(metadata.FailedChecks)
	if counted != uint64(rollup.Contexts.TotalCount) {
		return ActionMetadata{}, ErrProvider
	}
	switch strings.ToUpper(rollup.State) {
	case "EXPECTED", "PENDING":
		metadata.ChecksState = ChecksStatePending
	case "SUCCESS":
		metadata.ChecksState = ChecksStateSuccess
	case "ERROR", "FAILURE":
		metadata.ChecksState = ChecksStateFailure
	default:
		return ActionMetadata{}, ErrProvider
	}
	return metadata, nil
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

type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (client *Client) graphQL(
	ctx context.Context,
	credential Credential,
	query string,
	variables map[string]any,
) error {
	return client.graphQLData(ctx, credential, query, variables, nil)
}

func (client *Client) graphQLData(
	ctx context.Context,
	credential Credential,
	query string,
	variables map[string]any,
	output any,
) error {
	var response struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors"`
	}
	headers, err := client.do(ctx, credential, http.MethodPost, GraphQLPath,
		map[string]any{"query": query, "variables": variables}, &response)
	if err != nil {
		return err
	}
	if len(response.Errors) == 0 {
		if output == nil {
			return nil
		}
		if len(response.Data) == 0 || string(response.Data) == "null" ||
			json.Unmarshal(response.Data, output) != nil {
			return ErrProvider
		}
		return nil
	}
	if providerRateLimited(headers) {
		return &RateLimitError{RetryAfter: retryDuration(headers, client.now())}
	}
	for _, failure := range response.Errors {
		if graphQLErrorRateLimited(failure) {
			return &RateLimitError{
				RetryAfter: retryDuration(headers, client.now()),
			}
		}
		switch strings.ToUpper(failure.Type) {
		case "FORBIDDEN", "NOT_FOUND":
			return ErrPermissionDenied
		}
	}
	return ErrProvider
}

func graphQLErrorRateLimited(failure graphQLError) bool {
	if strings.EqualFold(failure.Type, "RATE_LIMITED") {
		return true
	}
	return secondaryRateLimitMessage(failure.Message)
}
