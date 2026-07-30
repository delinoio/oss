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
	releaseMutation := client.mutationPRs.acquire(reference)
	defer releaseMutation()
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
	type checkContext struct {
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		State      string `json:"state"`
	}
	type responseData struct {
		Node *struct {
			ReviewDecision    string `json:"reviewDecision"`
			StatusCheckRollup *struct {
				State    string `json:"state"`
				Contexts struct {
					TotalCount uint32         `json:"totalCount"`
					Nodes      []checkContext `json:"nodes"`
					PageInfo   struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"contexts"`
			} `json:"statusCheckRollup"`
		} `json:"node"`
	}
	var cursor any
	firstPage := true
	seenCursors := make(map[string]struct{})
	var reviewDecision string
	var rollupState string
	var totalCount uint32
	metadata.PendingChecks = 0
	metadata.SuccessfulChecks = 0
	metadata.FailedChecks = 0
	for {
		var data responseData
		err := client.graphQLData(ctx, credential, `
query($id:ID!,$cursor:String){
  node(id:$id){
    ... on PullRequest{
      reviewDecision
      statusCheckRollup{
        state
        contexts(first:100,after:$cursor){
          totalCount
          nodes{
            ... on CheckRun{status conclusion}
            ... on StatusContext{state}
          }
          pageInfo{hasNextPage endCursor}
        }
      }
    }
  }
}`, map[string]any{"id": metadata.NodeID, "cursor": cursor}, &data)
		if err != nil || data.Node == nil {
			return ActionMetadata{}, ErrProvider
		}
		rollup := data.Node.StatusCheckRollup
		if firstPage {
			reviewDecision = strings.ToUpper(data.Node.ReviewDecision)
			switch reviewDecision {
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
			if rollup == nil {
				metadata.ChecksState = ChecksStateUnknown
				return metadata, nil
			}
			rollupState = strings.ToUpper(rollup.State)
			totalCount = rollup.Contexts.TotalCount
		} else if rollup == nil ||
			strings.ToUpper(data.Node.ReviewDecision) != reviewDecision ||
			strings.ToUpper(rollup.State) != rollupState ||
			rollup.Contexts.TotalCount != totalCount {
			return ActionMetadata{}, ErrProvider
		}
		for _, check := range rollup.Contexts.Nodes {
			var target *uint32
			if check.Status != "" {
				switch strings.ToUpper(check.Status) {
				case "IN_PROGRESS", "PENDING", "QUEUED", "REQUESTED", "WAITING":
					target = &metadata.PendingChecks
				case "COMPLETED":
					switch strings.ToUpper(check.Conclusion) {
					case "NEUTRAL", "SKIPPED", "SUCCESS":
						target = &metadata.SuccessfulChecks
					case "ACTION_REQUIRED", "CANCELLED", "FAILURE", "STALE",
						"STARTUP_FAILURE", "TIMED_OUT":
						target = &metadata.FailedChecks
					default:
						return ActionMetadata{}, ErrProvider
					}
				default:
					return ActionMetadata{}, ErrProvider
				}
			} else {
				switch strings.ToUpper(check.State) {
				case "EXPECTED", "PENDING":
					target = &metadata.PendingChecks
				case "SUCCESS":
					target = &metadata.SuccessfulChecks
				case "ERROR", "FAILURE":
					target = &metadata.FailedChecks
				default:
					return ActionMetadata{}, ErrProvider
				}
			}
			if *target == ^uint32(0) {
				return ActionMetadata{}, ErrProvider
			}
			*target++
		}
		if !rollup.Contexts.PageInfo.HasNextPage {
			break
		}
		nextCursor := rollup.Contexts.PageInfo.EndCursor
		if nextCursor == "" {
			return ActionMetadata{}, ErrProvider
		}
		if _, duplicate := seenCursors[nextCursor]; duplicate {
			return ActionMetadata{}, ErrProvider
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
		firstPage = false
	}
	counted := uint64(metadata.PendingChecks) +
		uint64(metadata.SuccessfulChecks) + uint64(metadata.FailedChecks)
	if counted != uint64(totalCount) {
		return ActionMetadata{}, ErrProvider
	}
	switch rollupState {
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
		return client.graphQLWithExpectedHead(ctx, credential, `
mutation($id:ID!,$method:PullRequestMergeMethod!,$head:GitObjectID!){
  enablePullRequestAutoMerge(input:{
    pullRequestId:$id,mergeMethod:$method,expectedHeadOid:$head
  }){
    pullRequest{id}
  }
}`, map[string]any{
			"id": metadata.NodeID, "method": mergeMethodGraphQL(mutation.MergeMethod),
			"head": metadata.HeadSHA,
		}, metadata.HeadSHA)
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
	return client.graphQLDataWithExpectedHead(
		ctx, credential, query, variables, nil, "")
}

func (client *Client) graphQLWithExpectedHead(
	ctx context.Context,
	credential Credential,
	query string,
	variables map[string]any,
	expectedHead string,
) error {
	return client.graphQLDataWithExpectedHead(
		ctx, credential, query, variables, nil, expectedHead)
}

func (client *Client) graphQLData(
	ctx context.Context,
	credential Credential,
	query string,
	variables map[string]any,
	output any,
) error {
	return client.graphQLDataWithExpectedHead(
		ctx, credential, query, variables, output, "")
}

func (client *Client) graphQLDataWithExpectedHead(
	ctx context.Context,
	credential Credential,
	query string,
	variables map[string]any,
	output any,
	expectedHead string,
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
		if expectedHead != "" &&
			graphQLExpectedHeadMismatch(failure, expectedHead) {
			return ErrStaleRevision
		}
		switch strings.ToUpper(failure.Type) {
		case "FORBIDDEN", "NOT_FOUND":
			return ErrPermissionDenied
		}
	}
	return ErrProvider
}

func graphQLExpectedHeadMismatch(
	failure graphQLError,
	expectedHead string,
) bool {
	switch strings.ToUpper(failure.Type) {
	case "CONFLICT", "UNPROCESSABLE":
	default:
		return false
	}
	message := strings.ToLower(failure.Message)
	if !strings.Contains(message, "expected") ||
		!strings.Contains(message, "head") ||
		!strings.Contains(message, "oid") {
		return false
	}
	return strings.Contains(message, "does not match") ||
		strings.Contains(message, "did not match") ||
		strings.Contains(message, "mismatch") ||
		(strings.Contains(message, strings.ToLower(expectedHead)) &&
			(strings.Contains(message, " but ") ||
				strings.Contains(message, "current") ||
				strings.Contains(message, "found") ||
				strings.Contains(message, "got")))
}

func graphQLErrorRateLimited(failure graphQLError) bool {
	if strings.EqualFold(failure.Type, "RATE_LIMITED") {
		return true
	}
	return secondaryRateLimitMessage(failure.Message)
}
