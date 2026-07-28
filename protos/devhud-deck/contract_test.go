package devhuddeck_test

import (
	"slices"
	"strings"
	"testing"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestCanonicalServicesAndMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		file    protoreflect.FileDescriptor
		service protoreflect.Name
		methods []string
	}{
		{
			file:    deckv1.File_devhud_deck_v1_view_proto,
			service: "DeckViewService",
			methods: []string{
				"ListViews",
				"GetView",
				"CreateView",
				"UpdateView",
				"DeleteView",
				"ListPullRequests",
				"GetManualRefreshQuote",
				"RefreshView",
				"MutatePullRequest",
				"DeleteFeatureData",
			},
		},
		{
			file:    deckv1.File_devhud_deck_v1_integration_proto,
			service: "DeckIntegrationService",
			methods: []string{
				"GetGitHubConnection",
				"StartGitHubConnection",
				"ListGitHubInstallations",
				"DisconnectGitHubConnection",
			},
		},
		{
			file:    deckv1.File_devhud_deck_v1_device_proto,
			service: "DeckDeviceService",
			methods: []string{
				"RegisterDevice",
				"UpdateDevice",
				"UnregisterDevice",
				"UpdateViewNotificationPreference",
				"ResolveNotificationEvent",
			},
		},
	}

	var serviceCount int
	for _, test := range tests {
		services := test.file.Services()
		serviceCount += services.Len()
		service := services.ByName(test.service)
		if service == nil {
			t.Fatalf("missing %s", test.service)
		}
		got := methodNames(service.Methods())
		if !slices.Equal(got, test.methods) {
			t.Errorf("%s methods = %v, want %v", test.service, got, test.methods)
		}
	}
	if serviceCount != 3 {
		t.Fatalf("service count = %d, want 3", serviceCount)
	}
}

func TestClosedRegistryAndLimits(t *testing.T) {
	t.Parallel()

	if got := deckv1.ViewKind_name; len(got) != 2 ||
		got[0] != "VIEW_KIND_UNSPECIFIED" ||
		got[1] != "VIEW_KIND_GITHUB_PULL_REQUESTS" {
		t.Errorf("view kinds = %v", got)
	}

	limits := map[deckv1.DeckLimit]int32{
		deckv1.DeckLimit_DECK_LIMIT_MAX_PERSONAL_VIEWS:       50,
		deckv1.DeckLimit_DECK_LIMIT_MAX_ORGANIZATION_VIEWS:   250,
		deckv1.DeckLimit_DECK_LIMIT_MAX_PULL_REQUEST_RESULTS: 500,
	}
	for limit, want := range limits {
		if got := int32(limit); got != want {
			t.Errorf("%s = %d, want %d", limit, got, want)
		}
	}

	closedEnums := map[string]int{
		"OwnerScope":              len(deckv1.OwnerScope_name),
		"ViewSort":                len(deckv1.ViewSort_name),
		"ViewGrouping":            len(deckv1.ViewGrouping_name),
		"PullRequestMutationKind": len(deckv1.PullRequestMutationKind_name),
		"ConnectionState":         len(deckv1.ConnectionState_name),
		"RefreshOutcome":          len(deckv1.RefreshOutcome_name),
		"NotificationTransition":  len(deckv1.NotificationTransition_name),
		"FreshnessState":          len(deckv1.FreshnessState_name),
	}
	for name, count := range closedEnums {
		if count < 2 {
			t.Errorf("%s has no concrete v1 values", name)
		}
	}
}

func TestCoreRepresentationShape(t *testing.T) {
	t.Parallel()

	common := deckv1.File_devhud_deck_v1_common_proto.Messages()
	uuid := common.ByName("UuidV7")
	if uuid == nil || uuid.Fields().ByName("value").Kind() != protoreflect.StringKind {
		t.Fatalf("UuidV7.value is not a string")
	}
	revision := common.ByName("Revision")
	for _, field := range []protoreflect.Name{"value", "etag"} {
		if revision.Fields().ByName(field) == nil {
			t.Errorf("Revision missing %s", field)
		}
	}

	viewMessages := deckv1.File_devhud_deck_v1_view_proto.Messages()
	query := viewMessages.ByName("ViewQuery")
	if query.Fields().ByName("raw_query") == nil ||
		query.Fields().ByName("builder") == nil {
		t.Error("ViewQuery must carry canonical raw and typed builder data")
	}
	builder := viewMessages.ByName("QueryBuilder")
	if builder.Fields().ByName("clauses") == nil ||
		builder.Fields().ByName("unrecognized_raw_clauses") == nil {
		t.Error("QueryBuilder cannot preserve unknown raw clauses")
	}

	update := viewMessages.ByName("UpdateViewInput")
	if update.Fields().ByName("owner") != nil || update.Fields().ByName("kind") != nil {
		t.Error("UpdateViewInput permits a view transfer or kind conversion")
	}
	if update.Fields().ByName("notification_preference") == nil {
		t.Error("UpdateViewInput cannot update view notification preferences")
	}

	listPullRequests := viewMessages.ByName("ListPullRequestsResponse")
	for _, field := range []protoreflect.Name{
		"pull_requests",
		"page",
		"truncated",
		"result_limit",
		"freshness",
		"view_revision",
	} {
		if listPullRequests.Fields().ByName(field) == nil {
			t.Errorf("ListPullRequestsResponse missing %s", field)
		}
	}
}

func TestPullRequestResultAndMutationShape(t *testing.T) {
	t.Parallel()

	messages := deckv1.File_devhud_deck_v1_view_proto.Messages()
	result := messages.ByName("PullRequestResult")
	for _, field := range []protoreflect.Name{
		"repository",
		"number",
		"title",
		"author",
		"review_decision",
		"checks",
		"mergeability",
		"is_draft",
		"updated_at",
		"revision",
	} {
		if result.Fields().ByName(field) == nil {
			t.Errorf("PullRequestResult missing %s", field)
		}
	}

	mutation := messages.ByName("PullRequestMutation")
	if mutation.Oneofs().Len() != 1 {
		t.Fatalf("PullRequestMutation oneofs = %d, want 1", mutation.Oneofs().Len())
	}
	if got := mutation.Oneofs().Get(0).Fields().Len(); got != 13 {
		t.Errorf("PullRequestMutation variants = %d, want 13", got)
	}
	merge := messages.ByName("MergePullRequestMutation")
	if merge.Fields().ByName("confirmed") == nil {
		t.Error("merge mutation is missing explicit confirmation")
	}
}

func TestDeviceNotificationAndWidgetShape(t *testing.T) {
	t.Parallel()

	messages := deckv1.File_devhud_deck_v1_device_proto.Messages()
	device := messages.ByName("Device")
	for _, field := range []protoreflect.Name{
		"device_id",
		"platform",
		"detailed_notification_text_enabled",
		"shortcuts",
		"widgets",
		"revision",
	} {
		if device.Fields().ByName(field) == nil {
			t.Errorf("Device missing %s", field)
		}
	}

	widget := messages.ByName("WidgetState")
	for _, field := range []protoreflect.Name{
		"widget_id",
		"view_id",
		"family",
		"privacy",
		"snapshot",
		"revision",
	} {
		if widget.Fields().ByName(field) == nil {
			t.Errorf("WidgetState missing %s", field)
		}
	}

	widgetConfiguration := messages.ByName("WidgetConfiguration")
	for _, field := range []protoreflect.Name{
		"widget_id",
		"view_id",
		"family",
		"privacy",
	} {
		if widgetConfiguration.Fields().ByName(field) == nil {
			t.Errorf("WidgetConfiguration missing %s", field)
		}
	}
	for _, field := range []protoreflect.Name{"snapshot", "revision"} {
		if widgetConfiguration.Fields().ByName(field) != nil {
			t.Errorf("WidgetConfiguration permits client-authored %s", field)
		}
	}
	for _, requestName := range []protoreflect.Name{
		"RegisterDeviceRequest",
		"UpdateDeviceRequest",
	} {
		request := messages.ByName(requestName)
		widgets := request.Fields().ByName("widgets")
		if widgets == nil || widgets.Message() != widgetConfiguration {
			t.Errorf("%s.widgets does not use WidgetConfiguration", requestName)
		}
	}

	registerResponse := messages.ByName("RegisterDeviceResponse")
	for index := range registerResponse.Fields().Len() {
		name := string(registerResponse.Fields().Get(index).Name())
		if strings.Contains(name, "grant") {
			t.Errorf("RegisterDeviceResponse leaks cleanup grant in field %q", name)
		}
	}
}

func TestStableErrorsAndCredentialsStayOutOfMessages(t *testing.T) {
	t.Parallel()

	requiredErrors := []deckv1.ErrorReason{
		deckv1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED,
		deckv1.ErrorReason_ERROR_REASON_GITHUB_PERMISSION_DENIED,
		deckv1.ErrorReason_ERROR_REASON_STALE_REVISION,
		deckv1.ErrorReason_ERROR_REASON_IDEMPOTENCY_CONFLICT,
		deckv1.ErrorReason_ERROR_REASON_PERSONAL_VIEW_LIMIT_REACHED,
		deckv1.ErrorReason_ERROR_REASON_ORGANIZATION_VIEW_LIMIT_REACHED,
		deckv1.ErrorReason_ERROR_REASON_RESULT_LIMIT_TRUNCATED,
		deckv1.ErrorReason_ERROR_REASON_BILLING_PREFLIGHT_REQUIRED,
		deckv1.ErrorReason_ERROR_REASON_BILLING_RESERVATION_FAILED,
		deckv1.ErrorReason_ERROR_REASON_PROVIDER_RATE_LIMITED,
		deckv1.ErrorReason_ERROR_REASON_PROVIDER_TIMEOUT,
		deckv1.ErrorReason_ERROR_REASON_DISCONNECTED,
		deckv1.ErrorReason_ERROR_REASON_UNSUPPORTED_GITHUB_HOST,
		deckv1.ErrorReason_ERROR_REASON_UNSUPPORTED_ACTION,
	}
	for _, reason := range requiredErrors {
		if reason == deckv1.ErrorReason_ERROR_REASON_UNSPECIFIED {
			t.Fatal("a required stable error resolved to unspecified")
		}
	}

	for _, file := range []protoreflect.FileDescriptor{
		deckv1.File_devhud_deck_v1_common_proto,
		deckv1.File_devhud_deck_v1_view_proto,
		deckv1.File_devhud_deck_v1_integration_proto,
		deckv1.File_devhud_deck_v1_device_proto,
	} {
		walkMessages(file.Messages(), func(message protoreflect.MessageDescriptor) {
			for index := range message.Fields().Len() {
				name := string(message.Fields().Get(index).Name())
				for _, forbidden := range []string{
					"authorization_header",
					"bearer",
					"client_secret",
					"forwarded_delibase",
					"github_token",
					"revocation_grant",
					"webhook_secret",
				} {
					if strings.Contains(name, forbidden) {
						t.Errorf("%s.%s contains credential field fragment %q", message.FullName(), name, forbidden)
					}
				}
			}
		})
	}
}

func methodNames(methods protoreflect.MethodDescriptors) []string {
	names := make([]string, 0, methods.Len())
	for index := range methods.Len() {
		names = append(names, string(methods.Get(index).Name()))
	}
	return names
}

func walkMessages(messages protoreflect.MessageDescriptors, visit func(protoreflect.MessageDescriptor)) {
	for index := range messages.Len() {
		message := messages.Get(index)
		visit(message)
		walkMessages(message.Messages(), visit)
	}
}
