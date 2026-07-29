package service

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/audit"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/rpcerr"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (service *View) ListPullRequestMutationCandidates(
	ctx context.Context,
	request *connect.Request[deckv1.ListPullRequestMutationCandidatesRequest],
) (*connect.Response[deckv1.ListPullRequestMutationCandidatesResponse], error) {
	_, view, _, reference, connection, err := service.authorizePullRequestAction(
		ctx, request.Msg.ViewId, request.Msg.PullRequest)
	if err != nil {
		return nil, err
	}
	kind := deckgithub.MutationKind(request.Msg.MutationKind)
	if kind != deckgithub.MutationAssignUsers &&
		kind != deckgithub.MutationRequestReviewers &&
		kind != deckgithub.MutationAddLabels {
		return nil, rpcerr.New(connect.CodeUnimplemented,
			deckv1.ErrorReason_ERROR_REASON_UNSUPPORTED_ACTION)
	}
	cursor, err := service.decodeProviderCursor(
		view.GetViewId().GetValue(), request.Msg.GetPage().GetCursor())
	if err != nil {
		return nil, err
	}
	page, err := service.dependencies.GitHubClient.ListMutationCandidates(
		ctx, connection.Installation.ID, connection.Credential,
		connection.Installation.Permissions, reference, kind,
		request.Msg.Query, deckgithub.Page{
			Cursor: cursor,
			Limit:  int(pageSize(request.Msg.Page, 50, 100)),
		})
	if err != nil {
		return nil, mapGitHubError(err)
	}
	candidates := make([]*deckv1.PullRequestMutationCandidate, 0,
		len(page.Candidates))
	for _, candidate := range page.Candidates {
		switch candidate.Kind {
		case deckgithub.CandidateUser:
			candidates = append(candidates, &deckv1.PullRequestMutationCandidate{
				Candidate: &deckv1.PullRequestMutationCandidate_User{
					User: &deckv1.GitHubUser{Login: candidate.User.Login},
				},
			})
		case deckgithub.CandidateTeam:
			candidates = append(candidates, &deckv1.PullRequestMutationCandidate{
				Candidate: &deckv1.PullRequestMutationCandidate_Team{
					Team: &deckv1.GitHubTeam{
						Organization: candidate.Team.Organization,
						Slug:         candidate.Team.Slug,
					},
				},
			})
		case deckgithub.CandidateLabel:
			candidates = append(candidates, &deckv1.PullRequestMutationCandidate{
				Candidate: &deckv1.PullRequestMutationCandidate_Label{
					Label: candidate.Label,
				},
			})
		}
	}
	next := service.encodeProviderCursor(
		view.GetViewId().GetValue(), page.NextCursor)
	return connect.NewResponse(
		&deckv1.ListPullRequestMutationCandidatesResponse{
			Candidates: candidates,
			Page:       &deckv1.PageResponse{NextCursor: next},
			PullRequestRevision: &deckv1.Revision{
				Value: page.Revision,
				Etag: service.pullRequestETag(
					viewIDValue(view), request.Msg.PullRequest, page.Revision),
			},
		}), nil
}

func (service *View) MutatePullRequest(
	ctx context.Context,
	request *connect.Request[deckv1.MutatePullRequestRequest],
) (*connect.Response[deckv1.MutatePullRequestResponse], error) {
	viewer, view, snapshot, reference, connection, err :=
		service.authorizePullRequestAction(
			ctx, request.Msg.ViewId, request.Msg.PullRequest)
	if err != nil {
		return nil, err
	}
	mutation, err := providerMutation(request.Msg.Mutation)
	if err != nil {
		return nil, err
	}
	expected := request.Msg.ExpectedRevision
	if expected == nil || expected.Value == 0 ||
		expected.Etag != service.pullRequestETag(
			viewIDValue(view), request.Msg.PullRequest, expected.Value) {
		return nil, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	result, err := service.dependencies.GitHubClient.Mutate(
		ctx, viewer.AccountID.String(), connection.Installation.ID,
		connection.Credential, connection.Installation.Permissions,
		reference, expected.Value, mutation)
	if err != nil {
		return nil, mapGitHubError(err)
	}
	ownerID, err := ownerID(view.Owner)
	if err != nil {
		return nil, err
	}
	ownerHash := service.dependencies.Hasher.Sum(
		"owner", view.Owner.Scope.String()+":"+ownerID.String())
	// The provider mutation has already completed. recordAudit reports its own
	// persistence failure, which must not turn provider success into RPC failure.
	_ = service.recordAudit(
		ctx, viewer.Subject, audit.EventPullRequestMutated, view.Owner.Scope,
		ownerHash[:], audit.ResourceView, viewIDValue(view),
		audit.OutcomeSuccess)
	response := &deckv1.MutatePullRequestResponse{
		MutationKind:    deckv1.PullRequestMutationKind(result.Kind),
		RefreshRequired: result.RefreshRequired,
	}
	if result.RefreshRequired {
		return connect.NewResponse(response), nil
	}
	detail := service.pullRequestDetail(
		viewIDValue(view), request.Msg.PullRequest, snapshot, result.Metadata)
	response.PullRequest = detail
	return connect.NewResponse(response), nil
}

func (service *View) authorizePullRequestAction(
	ctx context.Context,
	viewIDMessage *deckv1.UuidV7,
	referenceMessage *deckv1.PullRequestReference,
) (contractsViewer, *deckv1.View, *deckv1.PullRequestResult,
	deckgithub.PullRequestRef,
	database.GitHubConnectionRecord, error) {
	viewer, err := viewerFromContext(ctx)
	if err != nil {
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, err
	}
	viewID, err := parseUUID(viewIDMessage)
	if err != nil || referenceMessage == nil ||
		referenceMessage.Repository == nil || referenceMessage.Number == 0 {
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, rpcerr.New(
				connect.CodeInvalidArgument,
				deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	view, err := service.getAuthorizedView(ctx, viewer, viewID, false)
	if err != nil {
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, err
	}
	authorizedOwnerID, _ := ownerID(view.Owner)
	allowed, err := service.dependencies.Repositories.CanReadRepository(
		ctx, viewer, view.Owner, referenceMessage.Repository.Owner,
		referenceMessage.Repository.Name)
	if err != nil {
		if errors.Is(err, deckgithub.ErrReauthenticationRequired) {
			return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
				database.GitHubConnectionRecord{}, rpcerr.New(
					connect.CodeFailedPrecondition,
					deckv1.ErrorReason_ERROR_REASON_DISCONNECTED)
		}
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, rpcerr.New(
				connect.CodeUnavailable,
				deckv1.ErrorReason_ERROR_REASON_DEPENDENCY_UNAVAILABLE)
	}
	if !allowed {
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, rpcerr.New(
				connect.CodePermissionDenied,
				deckv1.ErrorReason_ERROR_REASON_GITHUB_PERMISSION_DENIED)
	}
	viewerHash := service.dependencies.Hasher.Sum(
		"snapshot-viewer", viewer.AccountID.String())
	snapshot, err := service.dependencies.Store.GetSnapshot(
		ctx, viewID, viewerHash, referenceMessage)
	if errors.Is(err, database.ErrNotFound) {
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, rpcerr.New(
				connect.CodePermissionDenied,
				deckv1.ErrorReason_ERROR_REASON_GITHUB_PERMISSION_DENIED)
	}
	if err != nil {
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, mapDatabaseError(err)
	}
	if service.dependencies.GitHubClient == nil {
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, rpcerr.New(
				connect.CodeUnavailable,
				deckv1.ErrorReason_ERROR_REASON_DEPENDENCY_UNAVAILABLE)
	}
	connection, err := service.dependencies.Store.GetGitHubConnection(
		ctx, int16(view.Owner.Scope), authorizedOwnerID, viewer.AccountID, true)
	if err != nil {
		if errors.Is(err, deckgithub.ErrPermissionDenied) ||
			errors.Is(err, database.ErrNotFound) {
			return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
				database.GitHubConnectionRecord{}, rpcerr.New(
					connect.CodeFailedPrecondition,
					deckv1.ErrorReason_ERROR_REASON_DISCONNECTED)
		}
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, mapDatabaseError(err)
	}
	connection, err = refreshGitHubConnectionCredential(
		ctx, service.dependencies.Store, service.dependencies.GitHubBroker,
		viewer.AccountID, connection, service.dependencies.Clock.Now().UTC())
	if err != nil {
		return contractsViewer{}, nil, nil, deckgithub.PullRequestRef{},
			database.GitHubConnectionRecord{}, mapGitHubError(err)
	}
	reference := deckgithub.PullRequestRef{
		Repository: deckgithub.Repository{
			Owner: referenceMessage.Repository.Owner,
			Name:  referenceMessage.Repository.Name,
		},
		Number: referenceMessage.Number,
	}
	return contractsViewer(viewer), view, snapshot, reference, connection, nil
}

// contractsViewer is an alias local to this file that keeps the multi-result
// authorization helper readable without weakening the contracts.Viewer type.
type contractsViewer = contracts.Viewer

func providerMutation(input *deckv1.PullRequestMutation) (deckgithub.Mutation, error) {
	if input == nil {
		return deckgithub.Mutation{}, rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	result := deckgithub.Mutation{}
	switch value := input.Mutation.(type) {
	case *deckv1.PullRequestMutation_AssignUsers:
		result.Kind = deckgithub.MutationAssignUsers
		result.Users = providerUsers(value.AssignUsers.Users)
	case *deckv1.PullRequestMutation_UnassignUsers:
		result.Kind = deckgithub.MutationUnassignUsers
		result.Users = providerUsers(value.UnassignUsers.Users)
	case *deckv1.PullRequestMutation_RequestReviewers:
		result.Kind = deckgithub.MutationRequestReviewers
		result.Users = providerUsers(value.RequestReviewers.Users)
		result.Teams = providerTeams(value.RequestReviewers.Teams)
	case *deckv1.PullRequestMutation_RemoveReviewers:
		result.Kind = deckgithub.MutationRemoveReviewers
		result.Users = providerUsers(value.RemoveReviewers.Users)
		result.Teams = providerTeams(value.RemoveReviewers.Teams)
	case *deckv1.PullRequestMutation_AddLabels:
		result.Kind = deckgithub.MutationAddLabels
		result.Labels = append([]string(nil), value.AddLabels.Labels...)
	case *deckv1.PullRequestMutation_RemoveLabels:
		result.Kind = deckgithub.MutationRemoveLabels
		result.Labels = append([]string(nil), value.RemoveLabels.Labels...)
	case *deckv1.PullRequestMutation_MarkDraft:
		result.Kind = deckgithub.MutationMarkDraft
	case *deckv1.PullRequestMutation_MarkReady:
		result.Kind = deckgithub.MutationMarkReady
	case *deckv1.PullRequestMutation_Close:
		result.Kind = deckgithub.MutationClose
	case *deckv1.PullRequestMutation_Reopen:
		result.Kind = deckgithub.MutationReopen
	case *deckv1.PullRequestMutation_Merge:
		result.Kind = deckgithub.MutationMerge
		result.MergeMethod = deckgithub.MergeMethod(value.Merge.Method)
		result.Confirmed = value.Merge.Confirmed
	case *deckv1.PullRequestMutation_EnableAutoMerge:
		result.Kind = deckgithub.MutationEnableAutoMerge
		result.MergeMethod = deckgithub.MergeMethod(value.EnableAutoMerge.Method)
	case *deckv1.PullRequestMutation_CancelAutoMerge:
		result.Kind = deckgithub.MutationCancelAutoMerge
	default:
		return deckgithub.Mutation{}, rpcerr.New(connect.CodeUnimplemented,
			deckv1.ErrorReason_ERROR_REASON_UNSUPPORTED_ACTION)
	}
	return result, nil
}

func providerUsers(values []*deckv1.GitHubUser) []deckgithub.User {
	result := make([]deckgithub.User, len(values))
	for index, value := range values {
		result[index] = deckgithub.User{Login: value.GetLogin()}
	}
	return result
}

func providerTeams(values []*deckv1.GitHubTeam) []deckgithub.Team {
	result := make([]deckgithub.Team, len(values))
	for index, value := range values {
		result[index] = deckgithub.Team{
			Organization: value.GetOrganization(), Slug: value.GetSlug(),
		}
	}
	return result
}

func (service *View) pullRequestDetail(
	viewID uuid.UUID,
	reference *deckv1.PullRequestReference,
	base *deckv1.PullRequestResult,
	metadata deckgithub.ActionMetadata,
) *deckv1.PullRequestDetail {
	supported := make([]deckv1.PullRequestMutationKind, 0,
		len(metadata.Supported))
	for kind := deckgithub.MutationAssignUsers; kind <= deckgithub.MutationCancelAutoMerge; kind++ {
		if metadata.Supported[kind] {
			supported = append(supported, deckv1.PullRequestMutationKind(kind))
		}
	}
	methods := make([]deckv1.MergeMethod, 0, len(metadata.AvailableMethods))
	for method := deckgithub.MergeMethodMerge; method <= deckgithub.MergeMethodRebase; method++ {
		if metadata.AvailableMethods[method] {
			methods = append(methods, deckv1.MergeMethod(method))
		}
	}
	lifecycle := deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_CLOSED
	if metadata.IsMerged {
		lifecycle = deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_MERGED
	} else if metadata.IsOpen {
		lifecycle = deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_OPEN
	}
	revision := &deckv1.Revision{
		Value: metadata.Revision,
		Etag:  service.pullRequestETag(viewID, reference, metadata.Revision),
	}
	result := proto.Clone(base).(*deckv1.PullRequestResult)
	result.Repository = reference.Repository
	result.Number = reference.Number
	result.Title = metadata.Title
	result.Author = &deckv1.PullRequestAuthor{Login: metadata.Author.Login}
	result.UpdatedAt = timestamppb.New(metadata.UpdatedAt)
	result.Reviewers = make([]*deckv1.PullRequestReviewer, 0,
		len(metadata.Reviewers)+len(metadata.ReviewerTeams))
	for _, reviewer := range metadata.Reviewers {
		result.Reviewers = append(result.Reviewers, &deckv1.PullRequestReviewer{
			Reviewer: &deckv1.PullRequestReviewer_User{
				User: &deckv1.GitHubUser{Login: reviewer.Login},
			},
		})
	}
	for _, team := range metadata.ReviewerTeams {
		result.Reviewers = append(result.Reviewers, &deckv1.PullRequestReviewer{
			Reviewer: &deckv1.PullRequestReviewer_Team{
				Team: &deckv1.GitHubTeam{
					Organization: team.Organization, Slug: team.Slug,
				},
			},
		})
	}
	result.Assignees = make([]*deckv1.GitHubUser, 0, len(metadata.Assignees))
	for _, assignee := range metadata.Assignees {
		result.Assignees = append(
			result.Assignees, &deckv1.GitHubUser{Login: assignee.Login})
	}
	result.Labels = append([]string(nil), metadata.Labels...)
	result.ReviewDecision = map[deckgithub.ReviewDecision]deckv1.ReviewDecision{
		deckgithub.ReviewDecisionUnknown:          deckv1.ReviewDecision_REVIEW_DECISION_UNSPECIFIED,
		deckgithub.ReviewDecisionRequired:         deckv1.ReviewDecision_REVIEW_DECISION_REVIEW_REQUIRED,
		deckgithub.ReviewDecisionChangesRequested: deckv1.ReviewDecision_REVIEW_DECISION_CHANGES_REQUESTED,
		deckgithub.ReviewDecisionApproved:         deckv1.ReviewDecision_REVIEW_DECISION_APPROVED,
	}[metadata.ReviewDecision]
	result.Checks = &deckv1.CheckSummary{
		State: map[deckgithub.ChecksState]deckv1.ChecksState{
			deckgithub.ChecksStateUnknown: deckv1.ChecksState_CHECKS_STATE_UNSPECIFIED,
			deckgithub.ChecksStatePending: deckv1.ChecksState_CHECKS_STATE_PENDING,
			deckgithub.ChecksStateSuccess: deckv1.ChecksState_CHECKS_STATE_SUCCESS,
			deckgithub.ChecksStateFailure: deckv1.ChecksState_CHECKS_STATE_FAILURE,
		}[metadata.ChecksState],
	}
	result.IsDraft = metadata.IsDraft
	result.LifecycleState = lifecycle
	result.Mergeability = func() deckv1.Mergeability {
		if metadata.MergeConflicting {
			return deckv1.Mergeability_MERGEABILITY_CONFLICTING
		}
		if metadata.MergeBlocked {
			return deckv1.Mergeability_MERGEABILITY_BLOCKED
		}
		if metadata.Mergeable {
			return deckv1.Mergeability_MERGEABILITY_MERGEABLE
		}
		return deckv1.Mergeability_MERGEABILITY_UNKNOWN
	}()
	result.Revision = revision
	result.SupportedMutations = supported
	result.AvailableMergeMethods = methods
	return &deckv1.PullRequestDetail{
		Result:             result,
		SupportedMutations: supported, AvailableMergeMethods: methods,
		Revision: revision,
	}
}

func (service *View) pullRequestETag(
	viewID uuid.UUID,
	reference *deckv1.PullRequestReference,
	revision uint64,
) string {
	key := ""
	if reference != nil && reference.Repository != nil {
		key = strings.ToLower(reference.Repository.Owner) + "/" +
			strings.ToLower(reference.Repository.Name) + "#" +
			strconv.FormatUint(reference.Number, 10)
	}
	resourceID := uuid.NewSHA1(viewID, []byte(key))
	return service.dependencies.Hasher.ETag(resourceID, revision)
}

func viewIDValue(view *deckv1.View) uuid.UUID {
	id, _ := uuid.Parse(view.GetViewId().GetValue())
	return id
}

func (service *View) decodeProviderCursor(viewID, cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	payload, err := service.dependencies.Hasher.DecodeCursor(
		"mutation-candidates:"+viewID, cursor, 4)
	if err != nil {
		return "", rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT)
	}
	return strconv.FormatUint(uint64(decodeOffset(payload)), 10), nil
}

func (service *View) encodeProviderCursor(viewID, cursor string) string {
	if cursor == "" {
		return ""
	}
	offset, err := strconv.ParseUint(cursor, 10, 32)
	if err != nil {
		return ""
	}
	return service.dependencies.Hasher.EncodeCursor(
		"mutation-candidates:"+viewID, encodeOffset(uint32(offset)))
}

func mapGitHubError(err error) error {
	var providerLimit *deckgithub.RateLimitError
	switch {
	case errors.Is(err, deckgithub.ErrUnsupportedHost):
		return rpcerr.New(connect.CodeInvalidArgument,
			deckv1.ErrorReason_ERROR_REASON_UNSUPPORTED_GITHUB_HOST)
	case errors.Is(err, deckgithub.ErrConfirmationRequired):
		return rpcerr.New(connect.CodeFailedPrecondition,
			deckv1.ErrorReason_ERROR_REASON_MERGE_CONFIRMATION_REQUIRED)
	case errors.Is(err, deckgithub.ErrBranchProtected):
		return rpcerr.New(connect.CodeFailedPrecondition,
			deckv1.ErrorReason_ERROR_REASON_BRANCH_PROTECTION_BLOCKED)
	case errors.Is(err, deckgithub.ErrStaleRevision):
		return rpcerr.Conflict(
			deckv1.ErrorReason_ERROR_REASON_STALE_REVISION, nil, nil)
	case errors.Is(err, deckgithub.ErrConcurrencyLimited):
		return rpcerr.New(connect.CodeResourceExhausted,
			deckv1.ErrorReason_ERROR_REASON_PROVIDER_CONCURRENCY_LIMITED)
	case errors.Is(err, deckgithub.ErrMutationRateLimited):
		return rpcerr.New(connect.CodeResourceExhausted,
			deckv1.ErrorReason_ERROR_REASON_RATE_LIMITED)
	case errors.As(err, &providerLimit):
		return rpcerr.RetryAfter(
			deckv1.ErrorReason_ERROR_REASON_PROVIDER_RATE_LIMITED,
			providerLimit.RetryAfter)
	case errors.Is(err, deckgithub.ErrRateLimited):
		return rpcerr.New(connect.CodeResourceExhausted,
			deckv1.ErrorReason_ERROR_REASON_PROVIDER_RATE_LIMITED)
	case errors.Is(err, deckgithub.ErrReauthenticationRequired):
		return rpcerr.New(connect.CodeFailedPrecondition,
			deckv1.ErrorReason_ERROR_REASON_DISCONNECTED)
	case errors.Is(err, deckgithub.ErrPermissionDenied),
		errors.Is(err, database.ErrInstallationOwned):
		return rpcerr.New(connect.CodePermissionDenied,
			deckv1.ErrorReason_ERROR_REASON_GITHUB_PERMISSION_DENIED)
	case errors.Is(err, deckgithub.ErrUnsupportedAction):
		return rpcerr.New(connect.CodeUnimplemented,
			deckv1.ErrorReason_ERROR_REASON_UNSUPPORTED_ACTION)
	default:
		return rpcerr.New(connect.CodeUnavailable,
			deckv1.ErrorReason_ERROR_REASON_PROVIDER_FAILED)
	}
}
