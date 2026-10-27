// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import {
  create,
  fromBinary,
  fromJson,
  toBinary,
  toJson,
} from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import {
  ClientService,
  DeviceAuthorizationService,
  OrganizationService,
  PriceRatesSchema,
  TokenUsageSchema,
} from "../src/index.js";

test("unknown usage and price remain absent across binary and JSON", () => {
  const unknown = fromBinary(
    TokenUsageSchema,
    toBinary(TokenUsageSchema, create(TokenUsageSchema)),
  );
  assert.equal(unknown.inputTokens, undefined);
  assert.deepEqual(toJson(TokenUsageSchema, unknown), {});
  const zero = fromBinary(
    TokenUsageSchema,
    toBinary(TokenUsageSchema, create(TokenUsageSchema, { inputTokens: 0n })),
  );
  assert.equal(zero.inputTokens, 0n);
  assert.deepEqual(toJson(TokenUsageSchema, zero), { inputTokens: "0" });
  assert.equal(create(PriceRatesSchema).input, undefined);
  assert.equal(create(PriceRatesSchema, { input: "0" }).input, "0");
});

test("generated descriptors are directly usable by Connect and Connect Query", async () => {
  const transport: Transport = {
    async unary(method, _signal, _timeout, _headers, input) {
      assert.equal(method.name, "BeginDeviceAuthorization");
      assert.equal((input as { deviceName: string }).deviceName, "test-device");
      return {
        stream: false,
        service: method.parent,
        method,
        header: new Headers(),
        trailer: new Headers(),
        message: create(method.output),
      };
    },
    async stream() {
      throw new Error("Unexpected stream");
    },
  };
  const client = createClient(DeviceAuthorizationService, transport);
  await client.beginDeviceAuthorization({ deviceName: "test-device" });
  const key = createConnectQueryKey({
    schema: OrganizationService.method.listOrganizations,
    input: {},
    cardinality: "finite",
  });
  assert.ok(key.length > 0);
  assert.equal(
    ClientService.method.revokeCLIKey.input.typeName,
    "aiprouter.v1.RevokeCLIKeyRequest",
  );
});

test("recipient invitation descriptors preserve selectors and lifecycle", async () => {
  const {
    InvitationService,
    AcceptInvitationRequestSchema,
    GetInvitationResponseSchema,
    LifecycleState,
    Role,
    ErrorReason,
  } = await import("../src/index.js");
  assert.equal(
    InvitationService.method.getInvitation.input.typeName,
    "aiprouter.v1.GetInvitationRequest",
  );
  assert.deepEqual(
    InvitationService.method.listMyInvitations.input.fields.map(
      (field) => field.name,
    ),
    ["page"],
  );
  const request = create(AcceptInvitationRequestSchema, {
    invitationId: "invitation",
  });
  assert.equal(request.token, "");
  assert.equal(
    fromBinary(
      AcceptInvitationRequestSchema,
      toBinary(AcceptInvitationRequestSchema, request),
    ).invitationId,
    "invitation",
  );
  for (const state of [
    LifecycleState.PENDING,
    LifecycleState.ACCEPTED,
    LifecycleState.CANCELED,
    LifecycleState.EXPIRED,
    LifecycleState.SUPERSEDED,
    LifecycleState.FAILED,
  ]) {
    const response = create(GetInvitationResponseSchema, {
      invitation: {
        id: "invitation",
        organizationName: "Organization",
        email: "recipient@example.test",
        role: Role.MEMBER,
        state,
      },
    });
    assert.deepEqual(
      fromBinary(
        GetInvitationResponseSchema,
        toBinary(GetInvitationResponseSchema, response),
      ),
      response,
    );
  }
  assert.ok(ErrorReason.INVITATION_EMAIL_MISMATCH);
});

test("usage and audit filters preserve explicit unknown independently of omission", async () => {
  const {
    ListUsageRequestSchema,
    GetUsageSummaryRequestSchema,
    ListDailyUsageRequestSchema,
    ListOrganizationAuditRequestSchema,
    ListInstanceAuditRequestSchema,
  } = await import("../src/index.js");
  for (const schema of [
    ListUsageRequestSchema,
    GetUsageSummaryRequestSchema,
    ListDailyUsageRequestSchema,
    ListOrganizationAuditRequestSchema,
    ListInstanceAuditRequestSchema,
  ]) {
    const unknown = create(schema, { outcome: 0 });
    assert.equal(fromBinary(schema, toBinary(schema, unknown)).outcome, 0);
    assert.equal(
      fromBinary(schema, toBinary(schema, create(schema))).outcome,
      undefined,
    );
  }
});

test("OAuth native callback and collection CAS descriptors are available", async () => {
  const {
    BeginProviderOAuthRequestSchema,
    ProviderOAuthMethod,
    ProviderOAuthSchema,
    ListPriceOverridesResponseSchema,
    SetPriceOverrideRequestSchema,
    DeletePriceOverrideResponseSchema,
  } = await import("../src/index.js");
  const input = create(BeginProviderOAuthRequestSchema, {
    method: ProviderOAuthMethod.NATIVE_CALLBACK,
    redirectUri: "aiprouter://callback",
    clientId: "client",
  });
  assert.deepEqual(
    fromBinary(
      BeginProviderOAuthRequestSchema,
      toBinary(BeginProviderOAuthRequestSchema, input),
    ),
    input,
  );
  assert.equal(
    create(ProviderOAuthSchema, { callbackState: "correlation" }).callbackState,
    "correlation",
  );
  assert.equal(
    create(ListPriceOverridesResponseSchema, { revision: "1" }).revision,
    "1",
  );
  assert.equal(
    create(SetPriceOverrideRequestSchema, { expectedRevision: "1" })
      .expectedRevision,
    "1",
  );
  assert.equal(
    create(DeletePriceOverrideResponseSchema, { revision: "2" }).revision,
    "2",
  );
});

test("provider catalog carries typed eligible OAuth methods without client inventory", async () => {
  const { ProviderDefinitionSchema, ProviderOAuthMethod } =
    await import("../src/index.js");
  const provider = create(ProviderDefinitionSchema, {
    id: "native-provider",
    oauthMethods: [ProviderOAuthMethod.NATIVE_CALLBACK],
  });
  assert.deepEqual(
    fromBinary(
      ProviderDefinitionSchema,
      toBinary(ProviderDefinitionSchema, provider),
    ).oauthMethods,
    [ProviderOAuthMethod.NATIVE_CALLBACK],
  );
  assert.deepEqual(create(ProviderDefinitionSchema).oauthMethods, []);
});

test("pool generic OAuth affinity requires explicit opt-in", async () => {
  const { PoolSchema, CreatePoolRequestSchema, UpdatePoolRequestSchema } =
    await import("../src/index.js");
  for (const schema of [
    PoolSchema,
    CreatePoolRequestSchema,
    UpdatePoolRequestSchema,
  ]) {
    assert.equal(create(schema).affinityEnabled, false);
    const enabled = create(schema, { affinityEnabled: true });
    assert.equal(
      fromBinary(schema, toBinary(schema, enabled)).affinityEnabled,
      true,
    );
  }
});

test("hosted helper settings preserve personal selectors and optional defaults", async () => {
  const { HostedSettingsSchema, HostedVisionBackend, HostedSettingsService } =
    await import("../src/index.js");
  const settings = create(HostedSettingsSchema, {
    search: {
      enabled: false,
      target: {
        selection: { case: "connectionId", value: "search-connection" },
      },
    },
    vision: {
      backend: HostedVisionBackend.ROUTED,
      selection: { case: "routeModel", value: "vision-alias" },
      maxDescriptionsPerTurn: 0,
    },
    images: {
      target: { selection: { case: "poolId", value: "media-pool" } },
      videoBridgeEnabled: true,
    },
  });
  const decoded = fromBinary(
    HostedSettingsSchema,
    toBinary(HostedSettingsSchema, settings),
  );
  assert.deepEqual(decoded, settings);
  assert.equal(decoded.search?.enabled, false);
  assert.equal(decoded.vision?.enabled, undefined);
  assert.equal(decoded.images?.bridgeEnabled, false);
  assert.equal(
    HostedSettingsService.method.getHostedSettings.input.typeName,
    "aiprouter.v1.GetHostedSettingsRequest",
  );
});

test("provider config preserves explicit false, replacement, and write-only header clearing", async () => {
  const {
    ProviderConfigurationSchema,
    ProviderPolicyOverridesSchema,
    ProviderConnectionSchema,
    ProviderWire,
    UpdateProviderConnectionRequestSchema,
  } = await import("../src/index.js");
  const policy = create(ProviderPolicyOverridesSchema, {
    dropResponsesReasoningItems: false,
  });
  const configuration = create(ProviderConfigurationSchema, {
    wire: ProviderWire.RESPONSES,
    modelWires: { model: ProviderWire.ANTHROPIC },
    responsesPath: "/custom/responses",
    minMaxOutputTokens: 1024n,
    policy,
  });
  assert.deepEqual(
    fromBinary(
      ProviderConfigurationSchema,
      toBinary(ProviderConfigurationSchema, configuration),
    ),
    configuration,
  );
  assert.equal(configuration.policy?.foldDeveloperRoleToSystem, undefined);
  assert.equal(configuration.policy?.dropResponsesReasoningItems, false);
  const absent = create(UpdateProviderConnectionRequestSchema);
  const clear = create(UpdateProviderConnectionRequestSchema, {
    configuration: {},
    customHeaders: {},
  });
  assert.equal(absent.configuration, undefined);
  assert.equal(absent.customHeaders, undefined);
  const decoded = fromBinary(
    UpdateProviderConnectionRequestSchema,
    toBinary(UpdateProviderConnectionRequestSchema, clear),
  );
  assert.ok(decoded.configuration);
  assert.deepEqual(decoded.customHeaders?.values, {});
  assert.equal(
    ProviderConnectionSchema.fields.some(
      (field) => field.name === "custom_headers",
    ),
    false,
  );
});

test("JEV configuration preserves absence, defaults, and target hints", async () => {
  const { UpdateRouteRequestSchema, RoutingStrategy, ProviderWire } =
    await import("../src/index.js");
  assert.equal(create(UpdateRouteRequestSchema).jevDecision, undefined);
  const route = create(UpdateRouteRequestSchema, {
    strategy: RoutingStrategy.JEV,
    jevDecision: {
      decisionConnectionId: "private",
      decisionModel: "alias",
      decisionServiceModel: "tev1",
    },
    targets: [
      {
        modelId: "candidate",
        reasoningEfforts: ["ultra"],
        modelProfile: "profile",
      },
    ],
  });
  const decoded = fromBinary(
    UpdateRouteRequestSchema,
    toBinary(UpdateRouteRequestSchema, route),
  );
  assert.deepEqual(decoded, route);
  assert.equal(decoded.jevDecision?.decisionTimeoutMs, 0);
  assert.equal(decoded.jevDecision?.sessionPinTtlSeconds, 0);
  assert.equal(ProviderWire.JEV_DECISION, 5);
});

test("model export metadata distinguishes unknown, negative evidence, and empty ladders", async () => {
  const { ModelSchema, ReasoningEffort, ReasoningInterleaving } =
    await import("../src/index.js");
  const absent = create(ModelSchema, { id: "unknown" });
  const negative = create(ModelSchema, {
    fastRowAvailable: false,
    supportsReasoningEffort: false,
    supportsThinkingToggle: false,
    defaultThinkingEnabled: false,
    supportsReasoningSummaries: false,
    supportsReasoning: false,
    supportsTools: false,
    reasoningEffortsKnown: true,
    reasoningInterleaving: ReasoningInterleaving.NONE,
  });
  const positive = create(ModelSchema, {
    fastRowAvailable: true,
    defaultReasoningEffort: ReasoningEffort.ULTRA,
    reasoningEfforts: [ReasoningEffort.ULTRA],
    reasoningEffortsKnown: true,
    contextLimit: 1050000n,
    maxInputTokens: 922000n,
    reasoningInterleaving: ReasoningInterleaving.REASONING_CONTENT,
  });
  for (const value of [absent, negative, positive])
    assert.deepEqual(
      fromBinary(ModelSchema, toBinary(ModelSchema, value)),
      value,
    );
  assert.equal(absent.fastRowAvailable, undefined);
  assert.equal(absent.maxInputTokens, undefined);
  assert.equal(absent.reasoningEffortsKnown, undefined);
  assert.equal(negative.fastRowAvailable, false);
  assert.deepEqual(negative.reasoningEfforts, []);
  assert.equal(negative.reasoningEffortsKnown, true);
  assert.notEqual(positive.maxInputTokens, positive.contextLimit);
  assert.equal(
    (toJson(ModelSchema, negative) as Record<string, unknown>).fastRowAvailable,
    false,
  );
});

test("Responses item ID repair is an optional object with tri-state controls", async () => {
  const { ProviderPolicyOverridesSchema } = await import("../src/index.js");
  for (const responsesItemIdRepair of [
    undefined,
    {},
    {
      message: ["message-id"],
      reasoning: ["reasoning-id"],
      repairInvalidIds: false,
      repairMissingTerminalIds: true,
    },
  ]) {
    const value = create(ProviderPolicyOverridesSchema, {
      responsesItemIdRepair,
    });
    const decoded = fromBinary(
      ProviderPolicyOverridesSchema,
      toBinary(ProviderPolicyOverridesSchema, value),
    );
    assert.deepEqual(decoded, value);
    assert.deepEqual(
      toJson(ProviderPolicyOverridesSchema, decoded),
      responsesItemIdRepair === undefined ? {} : { responsesItemIdRepair },
    );
  }
});

test("JEV quota tiers preserve omitted thresholds and explicit zero", async () => {
  const { JevDecisionConfigSchema } = await import("../src/index.js");
  assert.equal(create(JevDecisionConfigSchema).decisionQuotaSignals, false);
  for (const decisionQuotaTiers of [
    undefined,
    {},
    { moderate: 0, limited: 70, nearlyExhausted: 90 },
  ]) {
    const value = create(JevDecisionConfigSchema, {
      decisionQuotaSignals: true,
      decisionQuotaTiers,
    });
    const decoded = fromBinary(
      JevDecisionConfigSchema,
      toBinary(JevDecisionConfigSchema, value),
    );
    assert.deepEqual(decoded, value);
    assert.deepEqual(toJson(JevDecisionConfigSchema, decoded), {
      decisionQuotaSignals: true,
      ...(decisionQuotaTiers === undefined ? {} : { decisionQuotaTiers }),
    });
  }
});

test("fast policy retains explicit false and per-model false entries", async () => {
  const { ProviderPolicyOverridesSchema } = await import("../src/index.js");
  assert.equal(create(ProviderPolicyOverridesSchema).fastEnabled, undefined);
  const json = {
    fastEnabled: false,
    modelSupportsServiceTier: { disabled: false, enabled: true },
  };
  const value = fromJson(ProviderPolicyOverridesSchema, json);
  const decoded = fromBinary(
    ProviderPolicyOverridesSchema,
    toBinary(ProviderPolicyOverridesSchema, value),
  );
  assert.deepEqual(toJson(ProviderPolicyOverridesSchema, decoded), json);
  assert.equal(
    Object.hasOwn(decoded.modelSupportsServiceTier, "missing"),
    false,
  );
});

test("native pool settings preserve optional defaults, routes, and signed priority", async () => {
  const {
    PoolSchema,
    CreatePoolRequestSchema,
    UpdatePoolRequestSchema,
    NativePoolOptionsSchema,
    NativePoolStrategy,
    NativeQuotaWindow,
  } = await import("../src/index.js");
  const nativeOptions = {
    strategy: NativePoolStrategy.QUOTA,
    stickyLimit: 1,
    autoSwitchThreshold: 0,
    cacheAffinity: false,
    quotaWindow: NativeQuotaWindow.WEEKLY,
    excludedPlans: ["Free"],
    enabled: false,
    nativeMessages: false,
    routes: [
      {
        name: "claude",
        match: "claude-*",
        connectionIds: ["second", "first"],
        fallback: true,
      },
    ],
  };
  const pool = create(PoolSchema, {
    nativeOptions,
    members: [
      {
        connectionId: "first",
        priority: 2,
        nativeOptions: {
          priority: -100,
          autoSwitchThreshold: 0,
          useRemainingQuota: true,
          allowCredits: true,
        },
      },
    ],
  });
  assert.deepEqual(fromJson(PoolSchema, toJson(PoolSchema, pool)), pool);
  assert.deepEqual(fromBinary(PoolSchema, toBinary(PoolSchema, pool)), pool);
  assert.equal(create(NativePoolOptionsSchema).enabled, undefined);
  assert.equal(create(NativePoolOptionsSchema).autoSwitchThreshold, undefined);
  assert.equal(create(UpdatePoolRequestSchema).nativeOptions, undefined);
  assert.ok(
    create(UpdatePoolRequestSchema, { nativeOptions: {} }).nativeOptions,
  );
  assert.equal(
    create(CreatePoolRequestSchema, { nativeOptions }).nativeOptions?.enabled,
    false,
  );
  assert.equal(pool.members[0]?.priority, 2);
  assert.equal(pool.members[0]?.nativeOptions?.priority, -100);
});

test("native health options preserve zero, false, and explicit unspecified", async () => {
  const { NativePoolOptionsSchema, NativePinnedTransientPolicy } =
    await import("../src/index.js");
  const absent = create(NativePoolOptionsSchema);
  assert.equal(absent.upstreamFailoverThreshold, undefined);
  assert.equal(absent.failureWindow, undefined);
  assert.equal(absent.pinnedTransientPolicy, undefined);
  for (const pinnedTransientPolicy of [
    NativePinnedTransientPolicy.UNSPECIFIED,
    NativePinnedTransientPolicy.KEEP,
    NativePinnedTransientPolicy.DETOUR_NEW_THREADS,
  ]) {
    const value = create(NativePoolOptionsSchema, {
      upstreamFailoverThreshold: 0,
      failureWindow: false,
      pinnedTransientPolicy,
    });
    assert.deepEqual(
      fromBinary(
        NativePoolOptionsSchema,
        toBinary(NativePoolOptionsSchema, value),
      ),
      value,
    );
    assert.deepEqual(
      fromJson(NativePoolOptionsSchema, toJson(NativePoolOptionsSchema, value)),
      value,
    );
    assert.equal(
      fromJson(NativePoolOptionsSchema, toJson(NativePoolOptionsSchema, value))
        .upstreamFailoverThreshold,
      0,
    );
  }
});

test("Vertex project and location preserve optional selection", async () => {
  const { ProviderConfigurationSchema } = await import("../src/index.js");
  for (const fields of [
    {},
    { vertexProject: "", vertexLocation: "" },
    { vertexProject: "auto", vertexLocation: "global" },
    { vertexProject: "project-id", vertexLocation: "us-central1" },
  ]) {
    const value = create(ProviderConfigurationSchema, fields);
    assert.deepEqual(
      fromBinary(
        ProviderConfigurationSchema,
        toBinary(ProviderConfigurationSchema, value),
      ),
      value,
    );
    assert.deepEqual(toJson(ProviderConfigurationSchema, value), fields);
    assert.deepEqual(
      fromJson(
        ProviderConfigurationSchema,
        toJson(ProviderConfigurationSchema, value),
      ),
      value,
    );
  }
});

test("reasoning retention keeps omitted settings and explicit source-default zeros", async () => {
  const {
    RouteSchema,
    CreateRouteRequestSchema,
    UpdateRouteRequestSchema,
    ReasoningRetentionConfigSchema,
  } = await import("../src/index.js");
  assert.equal(create(ReasoningRetentionConfigSchema).enabled, false);
  for (const reasoningRetention of [
    undefined,
    {},
    { enabled: true, maxContextPercent: 0, maxTokens: 0 },
    { enabled: true, maxContextPercent: 25, maxTokens: 50000 },
  ]) {
    const value = create(RouteSchema, { reasoningRetention });
    assert.deepEqual(
      fromBinary(RouteSchema, toBinary(RouteSchema, value)),
      value,
    );
    assert.deepEqual(fromJson(RouteSchema, toJson(RouteSchema, value)), value);
    assert.deepEqual(
      create(CreateRouteRequestSchema, { reasoningRetention })
        .reasoningRetention,
      value.reasoningRetention,
    );
    assert.deepEqual(
      create(UpdateRouteRequestSchema, { reasoningRetention })
        .reasoningRetention,
      value.reasoningRetention,
    );
  }
});
