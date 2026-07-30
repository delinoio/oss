package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/google/uuid"
)

type usageCallRecorder struct {
	commits  int
	releases int
}

func (*usageCallRecorder) RefreshMeter(
	context.Context,
) (contracts.RefreshMeter, error) {
	return contracts.RefreshMeter{}, nil
}

func (*usageCallRecorder) ReserveRefresh(
	context.Context,
	string,
	*deckv1.BillingSelection,
	uuid.UUID,
	contracts.RefreshMeter,
) (contracts.UsageReservation, error) {
	return contracts.UsageReservation{}, nil
}

func (usage *usageCallRecorder) CommitRefresh(
	context.Context,
	string,
	uuid.UUID,
	uuid.UUID,
) error {
	usage.commits++
	return nil
}

func (usage *usageCallRecorder) ReleaseRefresh(
	context.Context,
	string,
	uuid.UUID,
	uuid.UUID,
) error {
	usage.releases++
	return nil
}

func TestRefreshTraceRequiresAnActiveCompatibleClient(t *testing.T) {
	t.Parallel()
	valid := []struct {
		origin deckv1.RefreshOrigin
		client deckv1.RefreshClientKind
	}{
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_MOBILE},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_OS_BACKGROUND_TASK},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_WIDGET,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_WIDGET},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_MOBILE},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_VIEW_OPEN,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_SHORTCUT,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_MOBILE},
	}
	for _, pair := range valid {
		if !validRefreshTrace(pair.origin, pair.client) {
			t.Fatalf("valid trace rejected: %v/%v", pair.origin, pair.client)
		}
	}
	invalid := []struct {
		origin deckv1.RefreshOrigin
		client deckv1.RefreshClientKind
	}{
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_UNSPECIFIED,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_WIDGET,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_OS_BACKGROUND_TASK},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_VIEW_OPEN,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_WIDGET},
		{deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC,
			deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_WIDGET},
	}
	for _, pair := range invalid {
		if validRefreshTrace(pair.origin, pair.client) {
			t.Fatalf("invalid trace accepted: %v/%v", pair.origin, pair.client)
		}
	}
}

func TestRefreshAttemptAccountingRecoveryStates(t *testing.T) {
	t.Parallel()
	if refreshAttemptNeedsOnlyAccounting(database.RefreshAttempt{
		State: database.RefreshAttemptCreated,
	}) {
		t.Fatal("created attempt skipped view authorization")
	}
	if !refreshAttemptNeedsOnlyAccounting(database.RefreshAttempt{
		State: database.RefreshAttemptReserved,
	}) {
		t.Fatal("unresolved reserved attempt did not resume release accounting")
	}
	if !refreshAttemptNeedsOnlyAccounting(database.RefreshAttempt{
		State:    database.RefreshAttemptReserved,
		Response: &deckv1.RefreshViewResponse{},
	}) {
		t.Fatal("undispatched pending response did not resume release accounting")
	}
	if !refreshAttemptNeedsOnlyAccounting(database.RefreshAttempt{
		State: database.RefreshAttemptDispatched,
	}) {
		t.Fatal("dispatched attempt did not resume commit accounting")
	}
}

func TestSynthesizedRefreshAccountingResponseNeedsNoRetainedViewState(
	t *testing.T,
) {
	t.Parallel()
	hasher, err := security.NewHasher(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	viewID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	response := synthesizedRefreshAccountingResponse(
		hasher,
		database.RefreshAttempt{
			ViewID:       viewID,
			ViewRevision: 7,
		},
	)
	if response.GetViewId().GetValue() != viewID.String() ||
		response.GetViewRevision().GetValue() != 7 ||
		response.GetOutcome() !=
			deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_FAILED ||
		response.GetBillingDisposition() !=
			deckv1.BillingDisposition_BILLING_DISPOSITION_RESERVED ||
		response.GetFreshness() !=
			deckv1.FreshnessState_FRESHNESS_STATE_UNSPECIFIED {
		t.Fatalf("synthesized accounting response = %#v", response)
	}
}

func TestRefreshFreshnessHasAnExactFiveMinuteBoundary(t *testing.T) {
	t.Parallel()
	refreshedAt := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	if got := refreshFreshness(
		refreshedAt.Add(5*time.Minute-time.Nanosecond), refreshedAt,
	); got != deckv1.FreshnessState_FRESHNESS_STATE_FRESH {
		t.Fatalf("fresh state = %v", got)
	}
	if got := refreshFreshness(
		refreshedAt.Add(5*time.Minute), refreshedAt,
	); got != deckv1.FreshnessState_FRESHNESS_STATE_STALE {
		t.Fatalf("boundary state = %v", got)
	}
	if got := refreshFreshness(
		refreshedAt, time.Time{},
	); got != deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED {
		t.Fatalf("never-refreshed state = %v", got)
	}
}

func TestDisconnectedRefreshIsFreeAndDisconnected(t *testing.T) {
	t.Parallel()
	viewID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	response := disconnectedRefreshResponse(&deckv1.View{
		ViewId: &deckv1.UuidV7{Value: viewID.String()},
		Revision: &deckv1.Revision{
			Value: 3,
		},
		ConnectionState: deckv1.ConnectionState_CONNECTION_STATE_DISCONNECTED,
	})
	if response.GetOutcome() !=
		deckv1.RefreshOutcome_REFRESH_OUTCOME_DISCONNECTED ||
		response.GetBillingDisposition() !=
			deckv1.BillingDisposition_BILLING_DISPOSITION_FREE_NOT_ELIGIBLE ||
		response.GetFreshness() !=
			deckv1.FreshnessState_FRESHNESS_STATE_DISCONNECTED ||
		response.GetResultCount() != 0 ||
		response.GetRefreshedAt() != nil {
		t.Fatalf("disconnected response = %#v", response)
	}
}

func TestAutomaticRequestsCoalesceAndManualRefreshBypassesCache(t *testing.T) {
	t.Parallel()
	for _, origin := range []deckv1.RefreshOrigin{
		deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_WIDGET,
	} {
		outcome, usesCache := refreshCacheOutcome(origin)
		if !usesCache ||
			outcome != deckv1.RefreshOutcome_REFRESH_OUTCOME_COALESCED ||
			cacheBilling(outcome) !=
				deckv1.BillingDisposition_BILLING_DISPOSITION_FREE_COALESCED {
			t.Fatalf("automatic cache policy for %v = %v/%v",
				origin, outcome, usesCache)
		}
	}
	for _, origin := range []deckv1.RefreshOrigin{
		deckv1.RefreshOrigin_REFRESH_ORIGIN_VIEW_OPEN,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_SHORTCUT,
	} {
		outcome, usesCache := refreshCacheOutcome(origin)
		if !usesCache ||
			outcome != deckv1.RefreshOutcome_REFRESH_OUTCOME_CACHE_HIT ||
			cacheBilling(outcome) !=
				deckv1.BillingDisposition_BILLING_DISPOSITION_FREE_CACHE_HIT {
			t.Fatalf("free cache policy for %v = %v/%v",
				origin, outcome, usesCache)
		}
	}
	if outcome, usesCache := refreshCacheOutcome(
		deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL,
	); usesCache ||
		outcome != deckv1.RefreshOutcome_REFRESH_OUTCOME_UNSPECIFIED {
		t.Fatalf("manual refresh used cache: %v/%v", outcome, usesCache)
	}
	now := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	if !refreshCacheAvailable(
		deckv1.RefreshOutcome_REFRESH_OUTCOME_COALESCED,
		true, time.Time{}, now,
	) {
		t.Fatal("second device did not coalesce after a failed first request")
	}
	if refreshCacheAvailable(
		deckv1.RefreshOutcome_REFRESH_OUTCOME_COALESCED,
		false, now.Add(-refreshCacheWindow), now,
	) {
		t.Fatal("automatic request coalesced at the expired boundary")
	}
}

func TestRefreshPreflightBindsEveryBillingAndTraceField(t *testing.T) {
	t.Parallel()
	hasher, err := security.NewHasher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	viewID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	requestID := uuid.MustParse("01900000-0000-7000-8000-000000000002")
	organizationID := uuid.MustParse("01900000-0000-7000-8000-000000000003")
	teamID := uuid.MustParse("01900000-0000-7000-8000-000000000004")
	meter := contracts.RefreshMeter{
		MeterID:        uuid.MustParse("01900000-0000-7000-8000-000000000005"),
		PriceVersionID: uuid.MustParse("01900000-0000-7000-8000-000000000006"),
		ServiceID:      uuid.MustParse("01900000-0000-7000-8000-000000000007"),
		USDMicros:      contracts.ProviderRefreshPriceUSDMicros,
	}
	view := &deckv1.View{
		ViewId: &deckv1.UuidV7{Value: viewID.String()},
		Billing: &deckv1.BillingSelection{
			OrganizationId: &deckv1.UuidV7{Value: organizationID.String()},
			TeamId:         &deckv1.UuidV7{Value: teamID.String()},
		},
	}
	now := time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC)
	token := encodeRefreshPreflight(
		hasher, "subject", viewID, requestID, organizationID, teamID, meter,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL,
		deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP,
		now.Add(refreshPreflightLifetime))
	if err := validateRefreshPreflight(
		hasher, token, "subject", view, requestID, meter,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL,
		deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP, now,
	); err != nil {
		t.Fatalf("valid preflight = %v", err)
	}
	if err := validateRefreshPreflight(
		hasher, token, "subject", view, requestID, meter,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC,
		deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP, now,
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("origin substitution = %v", err)
	}
	if err := validateRefreshPreflight(
		hasher, token, "subject", view, requestID, meter,
		deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL,
		deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP,
		now.Add(refreshPreflightLifetime),
	); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("expired preflight = %v", err)
	}
}

func TestProviderFailureAccountingIsTyped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want deckv1.RefreshOutcome
	}{
		{nil, deckv1.RefreshOutcome_REFRESH_OUTCOME_REFRESHED},
		{deckgithub.ErrConcurrencyLimited,
			deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_CONCURRENCY_LIMITED},
		{&deckgithub.RateLimitError{RetryAfter: time.Minute},
			deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_RATE_LIMITED},
		{deckgithub.ErrTimeout,
			deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_TIMEOUT},
		{deckgithub.ErrPermissionDenied,
			deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_PERMISSION_DENIED},
		{deckgithub.ErrReauthenticationRequired,
			deckv1.RefreshOutcome_REFRESH_OUTCOME_DISCONNECTED},
		{errors.New("fixture"),
			deckv1.RefreshOutcome_REFRESH_OUTCOME_PROVIDER_FAILED},
	}
	for _, test := range tests {
		if got := refreshOutcomeForProviderError(test.err); got != test.want {
			t.Fatalf("outcome for %v = %v, want %v", test.err, got, test.want)
		}
	}
}

func TestRefreshResultsAreTruncatedBeforeDerivedSnapshots(t *testing.T) {
	t.Parallel()
	results := make([]*deckv1.PullRequestResult, 501)
	retained, truncated := retainedRefreshResults(results)
	if len(retained) != 500 || !truncated {
		t.Fatalf("retained results = %d, truncated = %v",
			len(retained), truncated)
	}
	retained, truncated = retainedRefreshResults(results[:500])
	if len(retained) != 500 || truncated {
		t.Fatalf("exact-limit results = %d, truncated = %v",
			len(retained), truncated)
	}
}

func TestProviderDispatchCommitsAndUndispatchedWorkReleasesExactlyOnce(
	t *testing.T,
) {
	t.Parallel()
	organizationID := uuid.MustParse(
		"01900000-0000-7000-8000-000000000001")
	reservationID := uuid.MustParse(
		"01900000-0000-7000-8000-000000000002")
	usage := &usageCallRecorder{}
	disposition, err := finalizeRefreshReservation(
		context.Background(), usage, "forwarded", organizationID,
		reservationID, true)
	if err != nil ||
		disposition !=
			deckv1.BillingDisposition_BILLING_DISPOSITION_COMMITTED ||
		usage.commits != 1 || usage.releases != 0 {
		t.Fatalf("dispatched finalization = %v/%v calls=%#v",
			disposition, err, usage)
	}
	usage = &usageCallRecorder{}
	disposition, err = finalizeRefreshReservation(
		context.Background(), usage, "forwarded", organizationID,
		reservationID, false)
	if err != nil ||
		disposition !=
			deckv1.BillingDisposition_BILLING_DISPOSITION_RELEASED ||
		usage.commits != 0 || usage.releases != 1 {
		t.Fatalf("undispatched finalization = %v/%v calls=%#v",
			disposition, err, usage)
	}
}

func TestNotificationTransitionsAreTypedAndPreferenceFiltered(t *testing.T) {
	t.Parallel()
	previous := &deckv1.PullRequestResult{
		Repository: &deckv1.RepositoryReference{Owner: "acme", Name: "widget"},
		Number:     7,
		Checks: &deckv1.CheckSummary{
			State: deckv1.ChecksState_CHECKS_STATE_SUCCESS,
		},
		Mergeability:   deckv1.Mergeability_MERGEABILITY_UNKNOWN,
		LifecycleState: deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_OPEN,
	}
	current := &deckv1.PullRequestResult{
		Repository: previous.Repository, Number: previous.Number,
		Assignees: []*deckv1.GitHubUser{{Login: "octocat"}},
		Reviewers: []*deckv1.PullRequestReviewer{{
			Reviewer: &deckv1.PullRequestReviewer_User{
				User: &deckv1.GitHubUser{Login: "OCTOCAT"},
			},
		}},
		Checks: &deckv1.CheckSummary{
			State: deckv1.ChecksState_CHECKS_STATE_FAILURE,
		},
		Mergeability:   deckv1.Mergeability_MERGEABILITY_CONFLICTING,
		LifecycleState: deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_CLOSED,
	}
	want := []deckv1.NotificationTransition{
		deckv1.NotificationTransition_NOTIFICATION_TRANSITION_ASSIGNED,
		deckv1.NotificationTransition_NOTIFICATION_TRANSITION_REVIEW_REQUESTED,
		deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CHECKS_FAILED,
		deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CONFLICTED,
		deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CLOSED,
	}
	if got := notificationTransitions(
		previous, current, "octocat"); !reflect.DeepEqual(got, want) {
		t.Fatalf("transitions = %v, want %v", got, want)
	}
	writes := notificationWrites(
		[]*deckv1.PullRequestResult{previous},
		[]*deckv1.PullRequestResult{current},
		[]*deckv1.ViewNotificationPreference{{
			Enabled: true,
			Transitions: []deckv1.NotificationTransition{
				deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CONFLICTED,
			},
		}},
		"octocat")
	if len(writes) != 1 || writes[0].Transition !=
		deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CONFLICTED {
		t.Fatalf("filtered writes = %#v", writes)
	}
	if writes := notificationWrites(
		[]*deckv1.PullRequestResult{previous},
		[]*deckv1.PullRequestResult{current}, nil, "octocat",
	); len(writes) != 0 {
		t.Fatalf("notification without an active device opt-in = %#v", writes)
	}

	mergeable := &deckv1.PullRequestResult{
		Mergeability: deckv1.Mergeability_MERGEABILITY_MERGEABLE,
	}
	if got := notificationTransitions(
		&deckv1.PullRequestResult{}, mergeable, "",
	); !reflect.DeepEqual(got, []deckv1.NotificationTransition{
		deckv1.NotificationTransition_NOTIFICATION_TRANSITION_BECAME_MERGEABLE,
	}) {
		t.Fatalf("mergeable transition = %v", got)
	}
	merged := &deckv1.PullRequestResult{
		LifecycleState: deckv1.PullRequestLifecycleState_PULL_REQUEST_LIFECYCLE_STATE_MERGED,
	}
	if got := notificationTransitions(
		&deckv1.PullRequestResult{}, merged, "",
	); !reflect.DeepEqual(got, []deckv1.NotificationTransition{
		deckv1.NotificationTransition_NOTIFICATION_TRANSITION_MERGED,
	}) {
		t.Fatalf("merged transition = %v", got)
	}
	missing := previousOnlySnapshots(
		[]*deckv1.PullRequestResult{
			previous,
			{
				Repository: &deckv1.RepositoryReference{
					Owner: "acme", Name: "other",
				},
				Number: 8,
			},
		},
		[]*deckv1.PullRequestResult{current},
	)
	if len(missing) != 1 || missing[0].GetNumber() != 8 {
		t.Fatalf("previous-only snapshots = %#v", missing)
	}
	if !hasEnabledNotificationPreference(
		[]*deckv1.ViewNotificationPreference{{Enabled: true}}) {
		t.Fatal("active device notification preference was ignored")
	}
	if hasEnabledNotificationPreference(
		[]*deckv1.ViewNotificationPreference{{Enabled: false}}) {
		t.Fatal("disabled device notification preference was eligible")
	}
}

func TestRefreshImplementationHasNoSchedulerOrBackgroundAuthorization(
	t *testing.T,
) {
	t.Parallel()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path unavailable")
	}
	serverRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	var source strings.Builder
	for _, relative := range []string{
		"internal/service/refresh.go",
		"internal/database/refresh.go",
		"internal/delibase/client.go",
	} {
		content, err := os.ReadFile(filepath.Join(serverRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		source.Write(content)
	}
	for _, forbidden := range []string{
		"REALQA_STORAGE",
		"ReserveAuthorizedUsage",
		"CommitAuthorizedUsage",
		"ReleaseAuthorizedUsage",
		"BackgroundUsage",
		"context.WithoutCancel",
		"time.NewTicker",
		"time.Tick(",
	} {
		if strings.Contains(source.String(), forbidden) {
			t.Fatalf("refresh implementation contains %q", forbidden)
		}
	}
}
