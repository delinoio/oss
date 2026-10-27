// SPDX-License-Identifier: Apache-2.0
package aiprouterv1_test

import (
	"testing"

	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestUnknownUsageAndPricePresence(t *testing.T) {
	for _, known := range []bool{false, true} {
		value := &v1.TokenUsage{}
		if known {
			value.InputTokens = proto.Uint64(0)
		}
		wire, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.TokenUsage{}
		if err := proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if (decoded.InputTokens != nil) != known {
			t.Fatal("usage presence lost")
		}
		json, err := protojson.Marshal(decoded)
		if err != nil {
			t.Fatal(err)
		}
		if !known && string(json) != "{}" {
			t.Fatalf("unknown serialized as known: %s", json)
		}
	}
	rates := &v1.PriceRates{Input: proto.String("0")}
	wire, _ := proto.Marshal(rates)
	decoded := &v1.PriceRates{}
	if err := proto.Unmarshal(wire, decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Input == nil || decoded.Output != nil {
		t.Fatal("price presence lost")
	}
}

func TestEveryMethodDeclaresAuthenticationScope(t *testing.T) {
	files := []protoreflect.FileDescriptor{v1.File_aiprouter_v1_organization_proto, v1.File_aiprouter_v1_device_proto, v1.File_aiprouter_v1_provider_proto, v1.File_aiprouter_v1_usage_proto, v1.File_aiprouter_v1_operator_proto, v1.File_aiprouter_v1_hosted_proto}
	for _, file := range files {
		services := file.Services()
		for i := 0; i < services.Len(); i++ {
			service := services.Get(i)
			for j := 0; j < service.Methods().Len(); j++ {
				method := service.Methods().Get(j)
				scope, ok := proto.GetExtension(method.Options(), v1.E_AuthScope).(v1.AuthScope)
				if !ok || scope == v1.AuthScope_AUTH_SCOPE_UNSPECIFIED {
					t.Errorf("missing scope: %s", method.FullName())
				}
				if scope == v1.AuthScope_AUTH_SCOPE_PUBLIC && method.Name() != "BeginDeviceAuthorization" {
					t.Errorf("unexpected public method: %s", method.FullName())
				}
				if scope == v1.AuthScope_AUTH_SCOPE_INFERENCE_KEY && service.Name() != "ClientService" {
					t.Errorf("inference credential grants management: %s", method.FullName())
				}
				if scope == v1.AuthScope_AUTH_SCOPE_DEVICE_SECRET && service.Name() != "DeviceAuthorizationService" {
					t.Errorf("device credential grants management: %s", method.FullName())
				}
				if scope == v1.AuthScope_AUTH_SCOPE_ORGANIZATION_MEMBER || scope == v1.AuthScope_AUTH_SCOPE_ORGANIZATION_ADMINISTRATOR || scope == v1.AuthScope_AUTH_SCOPE_ORGANIZATION_OWNER {
					if method.Input().Fields().ByName("organization_id") == nil {
						t.Errorf("missing tenant selector: %s", method.FullName())
					}
				}
			}
		}
	}
}

func TestPrivateResourcesAndOperatorBoundary(t *testing.T) {
	for _, name := range []protoreflect.Name{"ProviderConnection", "Pool", "Route"} {
		message := v1.File_aiprouter_v1_provider_proto.Messages().ByName(name)
		for _, forbidden := range []protoreflect.Name{"api_key", "raw_key", "access_token", "refresh_token", "prompt", "response"} {
			if message.Fields().ByName(forbidden) != nil {
				t.Errorf("secret in read model %s.%s", name, forbidden)
			}
		}
	}
	service := v1.File_aiprouter_v1_operator_proto.Services().ByName("OperatorService")
	for i := 0; i < service.Methods().Len(); i++ {
		method := service.Methods().Get(i)
		for _, forbidden := range []protoreflect.Name{"organization_id", "user_id"} {
			if method.Input().Fields().ByName(forbidden) != nil {
				t.Errorf("operator inspection selector: %s", method.FullName())
			}
		}
	}
	request := (&v1.RevokeDeviceKeyRequest{}).ProtoReflect().Descriptor()
	if request.Fields().ByName("key_id") != nil {
		t.Fatal("device compensation may not select an arbitrary key")
	}
}

func TestRecipientInvitationAndDeviceContracts(t *testing.T) {
	service := v1.File_aiprouter_v1_organization_proto.Services().ByName("InvitationService")
	for _, name := range []protoreflect.Name{"GetInvitation", "ListMyInvitations", "AcceptInvitation"} {
		method := service.Methods().ByName(name)
		if method == nil {
			t.Fatalf("missing recipient method %s", name)
		}
		if proto.GetExtension(method.Options(), v1.E_AuthScope) != v1.AuthScope_AUTH_SCOPE_WEB_SESSION {
			t.Fatalf("recipient method must require session: %s", name)
		}
	}
	list := (&v1.ListMyInvitationsRequest{}).ProtoReflect().Descriptor()
	if list.Fields().Len() != 1 || list.Fields().ByName("page") == nil {
		t.Fatal("recipient listing must not accept a caller-selected email or user")
	}
	invite := (&v1.Invitation{}).ProtoReflect().Descriptor()
	for _, name := range []protoreflect.Name{"token", "raw_token", "acceptance_url"} {
		if invite.Fields().ByName(name) != nil {
			t.Fatalf("recipient list exposes token: %s", name)
		}
	}
	for _, state := range []v1.LifecycleState{v1.LifecycleState_LIFECYCLE_STATE_PENDING, v1.LifecycleState_LIFECYCLE_STATE_ACCEPTED, v1.LifecycleState_LIFECYCLE_STATE_CANCELED, v1.LifecycleState_LIFECYCLE_STATE_EXPIRED, v1.LifecycleState_LIFECYCLE_STATE_SUPERSEDED, v1.LifecycleState_LIFECYCLE_STATE_FAILED} {
		original := &v1.GetInvitationResponse{Invitation: &v1.Invitation{Id: "invite", OrganizationId: "org", OrganizationName: "Organization", Email: "recipient@example.test", Role: v1.Role_ROLE_MEMBER, State: state}}
		wire, err := proto.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.GetInvitationResponse{}
		if err = proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(original, decoded) {
			t.Fatal("invitation detail or lifecycle lost")
		}
	}
	for _, request := range []*v1.AcceptInvitationRequest{{Token: "link-token"}, {InvitationId: proto.String("invite")}} {
		wire, err := proto.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.AcceptInvitationRequest{}
		if err = proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(request, decoded) {
			t.Fatal("acceptance selector compatibility lost")
		}
	}
	for number := int32(1); number <= 6; number++ {
		original := &v1.DeviceAuthorization{State: v1.DeviceAuthorizationState(number)}
		wire, err := proto.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.DeviceAuthorization{}
		if err = proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.State != original.State {
			t.Fatal("browser device lifecycle lost")
		}
	}
}

func TestOptionalFiltersPreserveExplicitUnknown(t *testing.T) {
	messages := []proto.Message{&v1.ListUsageRequest{}, &v1.GetUsageSummaryRequest{}, &v1.ListDailyUsageRequest{}, &v1.ListOrganizationAuditRequest{}, &v1.ListInstanceAuditRequest{}}
	for _, message := range messages {
		field := message.ProtoReflect().Descriptor().Fields().ByName("outcome")
		if field == nil || !field.HasPresence() {
			t.Fatalf("missing outcome presence: %T", message)
		}
		for _, present := range []bool{false, true} {
			value := proto.Clone(message)
			if present {
				value.ProtoReflect().Set(field, protoreflect.ValueOfEnum(0))
			}
			wire, err := proto.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			decoded := message.ProtoReflect().New().Interface()
			if err = proto.Unmarshal(wire, decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.ProtoReflect().Has(field) != present {
				t.Fatalf("unknown outcome presence lost: %T", message)
			}
		}
	}
	if (&v1.AuditRecord{}).Outcome != v1.AuditOutcome_AUDIT_OUTCOME_UNSPECIFIED {
		t.Fatal("missing audit outcome must remain unknown")
	}
	if (&v1.InstanceHealth{}).UsageRecording != v1.HealthState_HEALTH_STATE_UNSPECIFIED {
		t.Fatal("missing recording health must remain unknown")
	}
}

func TestOAuthOptionsAndCollectionRevisionRoundTrip(t *testing.T) {
	values := []proto.Message{
		&v1.Pool{AffinityEnabled: true},
		&v1.CreatePoolRequest{AffinityEnabled: true},
		&v1.UpdatePoolRequest{AffinityEnabled: true},
		&v1.ProviderDefinition{Id: "native-provider", OauthMethods: []v1.ProviderOAuthMethod{v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_NATIVE_CALLBACK}},
		&v1.BeginProviderOAuthRequest{Method: v1.ProviderOAuthMethod_PROVIDER_O_AUTH_METHOD_NATIVE_CALLBACK.Enum(), RedirectUri: proto.String("aiprouter://callback"), LoginProvider: proto.String("identity-center"), SsoRegion: proto.String("region"), ApiRegion: proto.String("region"), StartUrl: proto.String("https://start.example.test"), BaseUrl: proto.String("https://api.example.test"), AuthBaseUrl: proto.String("https://auth.example.test"), PortalUrl: proto.String("https://portal.example.test"), RegisterUrl: proto.String("https://register.example.test"), ClientId: proto.String("client")},
		&v1.ProviderOAuth{CallbackState: proto.String("public-correlation-state")},
		&v1.ListPriceOverridesResponse{Revision: "collection-1"},
		&v1.SetPriceOverrideRequest{ExpectedRevision: "collection-1"},
		&v1.SetPriceOverrideResponse{Revision: "collection-2"},
		&v1.DeletePriceOverrideRequest{ExpectedRevision: "collection-2"},
		&v1.DeletePriceOverrideResponse{Revision: "collection-3"},
	}
	for _, value := range values {
		wire, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := value.ProtoReflect().New().Interface()
		if err = proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatalf("new fields lost: %T", value)
		}
	}
}

func TestHostedSettingsPresenceAndIndependentTargets(t *testing.T) {
	value := &v1.HostedSettings{
		Search: &v1.HostedSearchSettings{Enabled: proto.Bool(false), Backend: v1.HostedSearchBackend_HOSTED_SEARCH_BACKEND_EXA.Enum(), Target: &v1.HostedHelperTarget{Selection: &v1.HostedHelperTarget_ConnectionId{ConnectionId: "personal-search"}}, XSearch: &v1.HostedXSearchOptions{Enabled: true, AllowedXHandles: []string{"example"}}},
		Vision: &v1.HostedVisionSettings{Selection: &v1.HostedVisionSettings_RouteModel{RouteModel: "vision-alias"}, Backend: v1.HostedVisionBackend_HOSTED_VISION_BACKEND_ROUTED.Enum(), MaxDescriptionsPerTurn: proto.Uint32(0)},
		Images: &v1.HostedImageSettings{Target: &v1.HostedHelperTarget{Selection: &v1.HostedHelperTarget_PoolId{PoolId: "personal-media"}}, VideoBridgeEnabled: true},
	}
	wire, err := proto.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded := &v1.HostedSettings{}
	if err = proto.Unmarshal(wire, decoded); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(value, decoded) {
		t.Fatal("helper target, option presence or zero value lost")
	}
	if decoded.Search.Enabled == nil || decoded.Vision.Enabled != nil {
		t.Fatal("explicit disable must remain distinct from default")
	}
	if decoded.Images.BridgeEnabled {
		t.Fatal("image bridge must default off independently of video")
	}
	for _, message := range []protoreflect.MessageDescriptor{(&v1.GetHostedSettingsRequest{}).ProtoReflect().Descriptor(), (&v1.UpdateHostedSettingsRequest{}).ProtoReflect().Descriptor(), (&v1.HostedHelperTarget{}).ProtoReflect().Descriptor()} {
		for _, name := range []protoreflect.Name{"user_id", "api_key", "access_token", "parent_credential", "operator_credential"} {
			if message.Fields().ByName(name) != nil {
				t.Fatalf("hosted selection widens ownership: %s", name)
			}
		}
	}
}

func TestProviderPolicyPresenceAndWriteOnlyHeaders(t *testing.T) {
	policy := &v1.ProviderPolicyOverrides{}
	fields := policy.ProtoReflect().Descriptor().Fields()
	if fields.Len() != 19 {
		t.Fatalf("policy inventory changed: %d", fields.Len())
	}
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		if field.Name() == "model_supports_service_tier" {
			if field.Number() != 20 || !field.IsMap() || field.MapValue().Kind() != protoreflect.BoolKind {
				t.Fatal("model service tier overrides must remain a boolean map")
			}
			continue
		}
		if field.Name() == "responses_item_id_repair" {
			if field.Number() != 18 || field.Kind() != protoreflect.MessageKind || !field.HasPresence() {
				t.Fatal("repair policy must be a present message on new field 18")
			}
			continue
		}
		if !field.HasPresence() || field.Kind() != protoreflect.BoolKind {
			t.Fatalf("policy must be tri-state: %s", field.Name())
		}
		value := &v1.ProviderPolicyOverrides{}
		value.ProtoReflect().Set(field, protoreflect.ValueOfBool(false))
		wire, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.ProviderPolicyOverrides{}
		if err = proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !decoded.ProtoReflect().Has(field) || decoded.ProtoReflect().Get(field).Bool() {
			t.Fatalf("explicit false lost: %s", field.Name())
		}
	}
	for _, message := range []proto.Message{&v1.ProviderConnection{}, &v1.ProviderConfiguration{}} {
		if message.ProtoReflect().Descriptor().Fields().ByName("custom_headers") != nil {
			t.Fatalf("readable message exposes headers: %T", message)
		}
	}
	for _, value := range []*v1.UpdateProviderConnectionRequest{
		{}, {Configuration: &v1.ProviderConfiguration{}}, {ResetConfiguration: true}, {CustomHeaders: &v1.ProviderCustomHeaders{}},
		{Configuration: &v1.ProviderConfiguration{Wire: v1.ProviderWire_PROVIDER_WIRE_RESPONSES.Enum(), ModelWires: map[string]v1.ProviderWire{"model": v1.ProviderWire_PROVIDER_WIRE_ANTHROPIC}, ResponsesPath: proto.String("/custom/responses"), MinMaxOutputTokens: proto.Int64(1024), Policy: &v1.ProviderPolicyOverrides{DropResponsesReasoningItems: proto.Bool(false)}}},
	} {
		wire, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.UpdateProviderConnectionRequest{}
		if err = proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatal("config omission/replacement/reset or header clear lost")
		}
	}
}

func TestJevDecisionPresenceAndTargetHints(t *testing.T) {
	for _, value := range []*v1.UpdateRouteRequest{
		{}, {JevDecision: &v1.JevDecisionConfig{}},
		{Strategy: v1.RoutingStrategy_ROUTING_STRATEGY_JEV, JevDecision: &v1.JevDecisionConfig{DecisionConnectionId: "private-connection", DecisionModel: "decision-alias", DecisionServiceModel: "tev1", DecisionTimeoutMs: 4000, SessionPinTtlSeconds: 60}, Targets: []*v1.RouteTarget{{ModelId: "candidate", ReasoningEfforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}, ModelProfile: "candidate profile"}}},
	} {
		wire, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.UpdateRouteRequest{}
		if err = proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatal("JEV config presence or target hints lost")
		}
	}
	if v1.ProviderWire_PROVIDER_WIRE_JEV_DECISION.Number() != 5 {
		t.Fatal("JEV custom wire changed")
	}
}

func TestModelExportMetadataPresence(t *testing.T) {
	descriptor := (&v1.Model{}).ProtoReflect().Descriptor()
	for number := protoreflect.FieldNumber(8); number <= 18; number++ {
		field := descriptor.Fields().ByNumber(number)
		if field == nil || !field.HasPresence() {
			t.Fatalf("export metadata lacks presence: %d", number)
		}
	}
	values := []*v1.Model{
		{Id: "unknown"},
		{Id: "explicit-negative", FastRowAvailable: proto.Bool(false), SupportsReasoningEffort: proto.Bool(false), SupportsThinkingToggle: proto.Bool(false), DefaultThinkingEnabled: proto.Bool(false), SupportsReasoningSummaries: proto.Bool(false), ReasoningInterleaving: v1.ReasoningInterleaving_REASONING_INTERLEAVING_NONE.Enum(), SupportsReasoning: proto.Bool(false), SupportsTools: proto.Bool(false), ReasoningEffortsKnown: proto.Bool(true)},
		{Id: "declared", FastRowAvailable: proto.Bool(true), DefaultReasoningEffort: v1.ReasoningEffort_REASONING_EFFORT_ULTRA.Enum(), ReasoningEfforts: []v1.ReasoningEffort{v1.ReasoningEffort_REASONING_EFFORT_ULTRA}, ReasoningEffortsKnown: proto.Bool(true), MaxInputTokens: proto.Uint64(922000), ContextLimit: proto.Uint64(1050000), ReasoningInterleaving: v1.ReasoningInterleaving_REASONING_INTERLEAVING_REASONING_CONTENT.Enum()},
	}
	for _, value := range values {
		wire, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.Model{}
		if err = proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatal("binary metadata presence lost")
		}
		json, err := protojson.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded = &v1.Model{}
		if err = protojson.Unmarshal(json, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatal("JSON metadata presence lost")
		}
	}
	if values[0].FastRowAvailable != nil || values[0].ReasoningEffortsKnown != nil || values[0].MaxInputTokens != nil {
		t.Fatal("unknown metadata must remain absent")
	}
}

func TestResponsesItemIdRepairPolicyPresence(t *testing.T) {
	descriptor := (&v1.ProviderPolicyOverrides{}).ProtoReflect().Descriptor()
	if !descriptor.ReservedRanges().Has(15) {
		t.Fatal("retired scalar number must stay reserved")
	}
	for _, repair := range []*v1.ResponsesItemIdRepair{nil, {}, {Message: []string{"message-id"}, Reasoning: []string{"reasoning-id"}, RepairInvalidIds: proto.Bool(false), RepairMissingTerminalIds: proto.Bool(true)}} {
		value := &v1.ProviderPolicyOverrides{ResponsesItemIdRepair: repair}
		wire, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.ProviderPolicyOverrides{}
		if err := proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatalf("repair presence lost: %v", decoded)
		}
	}
}

func TestJevQuotaTierPresence(t *testing.T) {
	for _, tiers := range []*v1.JevQuotaTiers{nil, {}, {Moderate: proto.Float64(0), Limited: proto.Float64(70), NearlyExhausted: proto.Float64(90)}} {
		value := &v1.JevDecisionConfig{DecisionQuotaSignals: true, DecisionQuotaTiers: tiers}
		wire, err := proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.JevDecisionConfig{}
		if err := proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatalf("quota presence lost: %v", decoded)
		}
	}
	if (&v1.JevDecisionConfig{}).GetDecisionQuotaSignals() {
		t.Fatal("quota signals must default off")
	}
}

func TestFastPolicyFalseMapValues(t *testing.T) {
	value := &v1.ProviderPolicyOverrides{FastEnabled: proto.Bool(false), ModelSupportsServiceTier: map[string]bool{"disabled": false, "enabled": true}}
	for _, jsonWire := range []bool{false, true} {
		var wire []byte
		var err error
		if jsonWire {
			wire, err = protojson.Marshal(value)
		} else {
			wire, err = proto.Marshal(value)
		}
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.ProviderPolicyOverrides{}
		if jsonWire {
			err = protojson.Unmarshal(wire, decoded)
		} else {
			err = proto.Unmarshal(wire, decoded)
		}
		if err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatalf("explicit false lost: %v", decoded)
		}
		if _, ok := decoded.ModelSupportsServiceTier["missing"]; ok {
			t.Fatal("missing model must stay absent")
		}
	}
}

func TestNativePoolOptionsPresenceAndPriority(t *testing.T) {
	options := &v1.NativePoolOptions{
		Strategy:    v1.NativePoolStrategy_NATIVE_POOL_STRATEGY_RESET_FIRST,
		StickyLimit: proto.Uint32(1), AutoSwitchThreshold: proto.Uint32(0), CacheAffinity: proto.Bool(false),
		QuotaWindow: v1.NativeQuotaWindow_NATIVE_QUOTA_WINDOW_WEEKLY, ExcludedPlans: []string{"Free"},
		Enabled: proto.Bool(false), NativeMessages: proto.Bool(false),
		Routes: []*v1.NativeModelRoute{{Name: "claude", Match: "claude-*", ConnectionIds: []string{"second", "first"}, Fallback: true}},
	}
	member := &v1.PoolMember{ConnectionId: "first", Priority: 2, NativeOptions: &v1.NativePoolMemberOptions{Priority: proto.Int32(-100), AutoSwitchThreshold: proto.Uint32(0), UseRemainingQuota: true, AllowCredits: true}}
	// Encoding preserves settings independently; provider-specific combinations are validated by management.
	for _, value := range []proto.Message{&v1.Pool{NativeOptions: options, Members: []*v1.PoolMember{member}}, &v1.CreatePoolRequest{NativeOptions: options}, &v1.UpdatePoolRequest{}, &v1.UpdatePoolRequest{NativeOptions: &v1.NativePoolOptions{}}, &v1.UpdatePoolRequest{NativeOptions: options}} {
		for _, jsonWire := range []bool{false, true} {
			var wire []byte
			var err error
			if jsonWire {
				wire, err = protojson.Marshal(value)
			} else {
				wire, err = proto.Marshal(value)
			}
			if err != nil {
				t.Fatal(err)
			}
			decoded := value.ProtoReflect().New().Interface()
			if jsonWire {
				err = protojson.Unmarshal(wire, decoded)
			} else {
				err = proto.Unmarshal(wire, decoded)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(value, decoded) {
				t.Fatalf("native options lost: %v", decoded)
			}
		}
	}
	if (&v1.NativePoolOptions{}).Enabled != nil || (&v1.NativePoolOptions{}).AutoSwitchThreshold != nil {
		t.Fatal("source defaults must remain absent")
	}
	if member.Priority != 2 || *member.NativeOptions.Priority != -100 {
		t.Fatal("native and ordinal priority must stay independent")
	}
}

func TestNativeHealthPolicyPresence(t *testing.T) {
	for _, value := range []*v1.NativePoolOptions{{}, {UpstreamFailoverThreshold: proto.Uint32(0), FailureWindow: proto.Bool(false), PinnedTransientPolicy: v1.NativePinnedTransientPolicy_NATIVE_PINNED_TRANSIENT_POLICY_UNSPECIFIED.Enum()}, {UpstreamFailoverThreshold: proto.Uint32(20), FailureWindow: proto.Bool(true), PinnedTransientPolicy: v1.NativePinnedTransientPolicy_NATIVE_PINNED_TRANSIENT_POLICY_DETOUR_NEW_THREADS.Enum()}} {
		for _, jsonWire := range []bool{false, true} {
			var wire []byte
			var err error
			if jsonWire {
				wire, err = protojson.Marshal(value)
			} else {
				wire, err = proto.Marshal(value)
			}
			if err != nil {
				t.Fatal(err)
			}
			decoded := &v1.NativePoolOptions{}
			if jsonWire {
				err = protojson.Unmarshal(wire, decoded)
			} else {
				err = proto.Unmarshal(wire, decoded)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(value, decoded) {
				t.Fatalf("native health presence lost: %v", decoded)
			}
		}
	}
}

func TestVertexConfigurationPresence(t *testing.T) {
	for _, value := range []*v1.ProviderConfiguration{{}, {VertexProject: proto.String(""), VertexLocation: proto.String("")}, {VertexProject: proto.String("auto"), VertexLocation: proto.String("global")}, {VertexProject: proto.String("project-id"), VertexLocation: proto.String("us-central1")}} {
		wire, err := protojson.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded := &v1.ProviderConfiguration{}
		if err := protojson.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatalf("Vertex JSON presence lost: %v", decoded)
		}
		wire, err = proto.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded.Reset()
		if err := proto.Unmarshal(wire, decoded); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(value, decoded) {
			t.Fatalf("Vertex binary presence lost: %v", decoded)
		}
	}
}

func TestReasoningRetentionPresence(t *testing.T) {
	for _, config := range []*v1.ReasoningRetentionConfig{nil, {}, {Enabled: true, MaxContextPercent: proto.Float64(0), MaxTokens: proto.Uint32(0)}, {Enabled: true, MaxContextPercent: proto.Float64(25), MaxTokens: proto.Uint32(50000)}} {
		for _, value := range []proto.Message{&v1.Route{ReasoningRetention: config}, &v1.CreateRouteRequest{ReasoningRetention: config}, &v1.UpdateRouteRequest{ReasoningRetention: config}} {
			for _, jsonWire := range []bool{false, true} {
				var wire []byte
				var err error
				if jsonWire {
					wire, err = protojson.Marshal(value)
				} else {
					wire, err = proto.Marshal(value)
				}
				if err != nil {
					t.Fatal(err)
				}
				decoded := value.ProtoReflect().New().Interface()
				if jsonWire {
					err = protojson.Unmarshal(wire, decoded)
				} else {
					err = proto.Unmarshal(wire, decoded)
				}
				if err != nil {
					t.Fatal(err)
				}
				if !proto.Equal(value, decoded) {
					t.Fatalf("retention presence lost: %v", decoded)
				}
			}
		}
	}
	if (&v1.ReasoningRetentionConfig{}).ProtoReflect().Descriptor().Fields().ByName("context_window") != nil {
		t.Fatal("context window must not be user configurable")
	}
}
