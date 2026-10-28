# Providers, OAuth, and routing

## Availability

The console provides Providers, Pools, and Routing pages backed by the service. A complete provider/authentication matrix and live account verification are not established here. Do not assume that a provider name in the compatibility reference means that your release supports it. Use a provider only when its release documentation supplies the authentication method, model identifiers, callback requirements, and supported protocol behavior.

## Connect personal providers

1. Select the organization where you will use the provider.
2. Open Providers, select **Connect provider**, and search the catalog. Choose the integration and authentication method.
3. For API-key authentication, supply your own provider key through the connection workflow.
4. For OAuth, use the integration's authorization flow and verify the destination and requested permissions. Follow its exact callback registration requirements. OIDC sign-in and provider OAuth are separate connections.
5. Check connection status with a non-sensitive request and confirm model availability.
6. Repeat setup separately in another organization if needed.

Never ask an owner, administrator, or operator to copy another member's provider credentials. Provider callbacks, OAuth client registrations, refresh behavior, and device/browser constraints are provider-specific. Exact provider instructions remain unavailable until the integration supplies them; do not invent a generic callback.

## Provider authorization in the console

For an OAuth integration, select **Continue with OAuth**, choose an authentication method offered for that provider, and supply any requested method-specific settings. Use **Open provider sign-in** only after verifying its destination and requested permissions. OIDC login and provider authorization remain separate flows.

For device or browser-poll methods, enter the displayed user code at the provider if requested and keep the authorization dialog open to see the result. The console respects the service's updated polling interval. For a method that requires manual completion, paste the returned authorization code, token, or full callback URL into the masked completion field. Supply authorization state if the dialog requires it. This server does not open a local callback listener; do not invent or register a generic callback route.

Closing the dialog does not cancel the flow. Use **Cancel authorization** to cancel explicitly. A completed flow creates or reconnects your scoped provider connection; expired, canceled, failed, and unavailable results are not successful connections. Completion and refresh can trigger model discovery, but missing model evidence is not proof of account entitlement.

OAuth completion, cancellation, refresh, and model discovery are implemented for the corresponding supported provider methods. Provider-specific restrictions still apply. If a connection reports **Authorization required**, reconnect through the explicit authorization workflow rather than repeatedly retrying an uncertain credential rotation. Automated provider fixtures do not establish live login, refresh, or native callback acceptance.

## Google Vertex project and location

Connect **Google Vertex** using your own supported credential in the selected organization. In the connection editor, expand **Advanced connection settings** and find **Vertex AI**. Select **Override vertex project** or **Override vertex location** to enter **Vertex project** or **Vertex location**. These fields apply only to Google Vertex connections.

For an OAuth connection, complete authorization first, then edit the resulting connection to set its advanced configuration. OAuth and service-account credentials require an explicit project and location, supplied by that personal credential or its connection overrides. They reject `auto`. TaiRouter does not use the service host's ambient Google identity or project when your connection omits a value.

For an API-key connection, choose the setup that matches your key:

| Project setup                                          | Behavior                                                                                                                                                                                                                                                                 |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| No project in the personal credential or configuration | Uses the Vertex express endpoint. A location does not turn an express request into a project request.                                                                                                                                                                    |
| Explicit project ID or project number                  | Uses the project endpoint. If no location is supplied, the location defaults to `global`.                                                                                                                                                                                |
| Explicit lowercase `auto`                              | Uses a bounded, read-only discovery request to Google with your selected API key. If discovery identifies its project, uses that project and your selected location, or `global` if omitted. If discovery returns no usable project, falls back to the express endpoint. |

Automatic project discovery has a 10-second probe limit. It does not grant project access or bypass destination restrictions, credential revocation, or request cancellation. Such authorization failures stop the request rather than authorize a fallback. An express fallback still requires a key and model that support that endpoint; discovery does not guarantee a successful inference request.

Use a valid Google location such as `global` or `us-central1`, not a URL. Location values use lowercase letters, digits, and internal hyphens. The server validates project and location values. A checked override with an empty value is explicitly empty and is rejected; uncheck the override to omit it. Omission uses the selected personal credential's metadata where available, then the defaults above.

For an existing connection, **Keep saved configuration** preserves its configuration. Editing a setting selects replacement of the complete configuration, retaining the other values shown in the form. Unchecking an override removes that override when the replacement is saved. **Reset configuration to provider defaults** removes all configuration overrides, not just Vertex project and location. Review the other settings before selecting **Save connection**. These implemented workflows and automated checks do not establish live Google authorization, project access, or model acceptance.

## Pools, models, and combinations

Add only your own accounts to your personal pool in the selected organization. Select the model and configure routing and combination fallbacks using that release's supported model identifiers and rules. Check streaming, tool calling, continuation, and error behavior for the chosen client/provider pair, rather than testing only a simple text response.

In Pools, select **Create pool**, name it, choose the strategy, and add your own connections in the intended order. In Routing, select **Create route**, enter the model alias used by your clients, choose a strategy, and add connection or pool targets with model IDs. Direct routing requires one target. Review target order and any weights before saving. Resolve unavailable targets and dependent routes before disconnecting a provider.

Pools support priority, round-robin, and least-recently-used selection. Routes support direct, fallback, round-robin, weighted, and JEV decision selection. These strategies now have server selection support; use positive weights for weighted targets and do not add duplicate pool members or route targets. Direct routes require exactly one target.

Keep a pool's provider and transport compatible. An incompatible pool is rejected; use separate route targets for cross-provider fallback. Paused, expired, uncertain, or cooled-down accounts can be ineligible even when listed in your settings. Bound continuation/recovery cannot silently rotate to another account. Strategy availability does not establish every provider-specific retry, quota, or probe behavior.

To opt in to generic OAuth session affinity, select **Keep generic OAuth sessions on the same account** in the pool editor. The default is off. Supported generic sessions can retain account affinity for 24 hours within their key/provider/session scope; Codex and Anthropic retain provider-specific session rules. Affinity does not override current authorization, account eligibility, or revocation. Deselect the option and save to remove the opt-in.

Model discovery can be unavailable for a custom or uncatalogued provider. The routing editor permits an explicit model ID; obtain it from the actual provider, not a guessed alias. No last-verified timestamp is available for provider connections: **Added** identifies creation, not successful verification.

Retries depend on the provider, authentication method, error, and whether an upstream send may already have occurred. There is no universal three-attempt provider retry rule. Do not replay an ambiguous send blindly. Cancellation, membership removal, key revocation, or loss of database authorization must stop additional upstream sends.

Public HTTPS providers are permitted by default. A local/private HTTP model server needs operator approval. Running Claude CLI, CodeBuddy, Qoder, or another local program as the provider backend is excluded, including execution agents and remote workspace/command dispatch. Configuring such a client to use a supported remote inference interface is a separate capability.

## Native OAuth pools

Native OAuth pool controls apply only when every member belongs to one supported provider family: **OpenAI (Codex)**, **Anthropic**, or **Anthropic 2**. Keep the two Anthropic families in separate pools. These controls govern upstream account selection; they are separate from generic OAuth session affinity and optional desktop TLS compatibility.

In the pool editor, expand **Native OAuth pool settings**, then select **Configure pool-level native settings**. Review **Native pooling** explicitly:

- On creation of a new pool with native settings, Default enables native pooling. Disabled explicitly opts out.
- For an existing or imported pool, unspecified enablement is disabled. Choose Enabled to opt in; do not assume the creation default is reapplied on an update.
- Saving replaces the complete pool configuration. Removing pool-level native settings clears them. Empty fields, explicit zero, and explicit Disabled can have different effects.

### Selection and switching

| Control                      | Default and meaning                                                                                                                                                |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Native selection strategy    | Default selects quota. Alternatives are round-robin and fill-first; reset-first is Codex-only. Selection remains subject to current account and model eligibility. |
| Sticky account limit         | Empty uses 1; accepted values are 1–100. This does not guarantee affinity after revocation, exhaustion, or other ineligibility.                                    |
| Pool switching threshold (%) | Empty uses 80 percent used quota. Accepted values are 0–100. Zero disables voluntary quota switching, not authorization or hard exhaustion checks.                 |
| Cache affinity               | Codex-only; Default is enabled. Explicit Disabled turns it off.                                                                                                    |
| Excluded plans               | Codex-only; one plan per line, up to 100. Plan names are matched without regard to case.                                                                           |
| Native quota window          | Anthropic-only: five-hour, weekly, or maximum utilization. Default is five-hour. Selecting weekly does not bypass an exhausted five-hour window.                   |
| Native messages              | Anthropic-only; Default is enabled. Disabled selects legacy translation.                                                                                           |

Review member options separately. An empty member switching threshold inherits the pool value; an explicit member value overrides it. **Use remaining quota** makes that member's effective switching threshold zero. Native priority accepts −100 to 100, with higher values first. It is separate from the ordinary pool order, whose lower priority values come first; leaving native priority empty preserves that ordinary ordering.

Codex members also have **Allow credits**, off by default. Enabling it permits use of available spendable provider credits where supported; it does not grant credits, guarantee availability, or bypass provider/account restrictions. Unknown quota is not a measured zero, and a configured threshold is not an entitlement claim.

### Codex upstream health controls

Open **Native OAuth pool settings** for a homogeneous OpenAI (Codex) OAuth pool to configure upstream health behavior. These controls are separate from quota switching thresholds:

- **Upstream failover threshold:** empty uses 3. Choose an integer from 0 to 20. Zero disables this consecutive-failure threshold; it does not disable the separate failure window, authorization checks, or provider quota restrictions.
- **Failure window:** Default is enabled and considers a sliding 60-second failure ratio alongside the consecutive-failure counter. Disabled uses only the consecutive-failure counter for this health policy. It does not convert credential, quota, or caller errors into transient upstream failures.
- **Pinned transient policy:** empty/default and **Keep** retain the manually pinned account for new threads under this transient-health policy. **Detour new threads** permits a new thread to use another eligible account when the manually pinned account is degraded. It does not move an existing pinned conversation, override account eligibility, or grant access to another user's credentials. An explicitly saved Unspecified value also behaves as Keep.

For example, threshold 0 with Failure window enabled leaves sliding-window health decisions active. Disabling the window leaves the configured consecutive-failure threshold in use. Neither setting guarantees that an upstream request succeeds or that a provider will accept a retry.

These fields are Codex-only. Anthropic and Anthropic 2 pools reject them even when explicitly set to zero, Disabled, or Unspecified; remove the Codex-only settings rather than treating those values as omission. Saving replaces the pool configuration, so removing an optional value returns it to its documented default. Automated checks cover these controls; no live Codex account or provider-health acceptance is claimed here.

### Ordered Anthropic model rules

For an Anthropic native pool, use **Ordered model rules** to restrict which personal pool connections serve matching models. Rules apply only while native pooling is enabled.

1. Give each rule a unique name beginning with a letter or digit and containing only letters, digits, underscores, or hyphens.
2. Enter a model pattern of at most 128 characters. Use `*` and `?` as wildcards.
3. Add the allowed connections from this pool and arrange rules in the intended order. The first matching rule wins.
4. Decide whether to select **Use the full pool only when no listed connection is eligible**. Without this explicit fallback, a matching rule with no eligible listed connection must not widen to the full pool.
5. Review and save the complete configuration. Resolve unavailable connections rather than assuming they will be replaced.

If no model rule matches, ordinary eligible-pool selection applies. A fallback choice never adds accounts from another user, organization, or provider family. These controls and automated fixtures do not establish complete provider-specific quota behavior or live account acceptance.

## Fast mode policy and model support

In a provider connection's configuration controls, **Fast mode policy** offers Inherit, Enabled, and Disabled. Inherit uses the provider policy; Disabled denies fast mode. Enabled does not guarantee lower latency and cannot create a fast capability for an unsupported model or API format.

Under **Model service-tier support**, add overrides using upstream model IDs, not route aliases. Keep the model's spelling: matching checks an exact ID first, then ignores letter case. Duplicate IDs that differ only by case are rejected. Each row can explicitly enable or disable support. Removing a row restores inherited policy; an explicit false value is not the same as removing the override. A per-model Enabled value cannot override a provider-wide denial or incompatible API format.

Choose an authorized fast entry actually returned by model discovery, when available. Do not manufacture a selector by appending a suffix to an arbitrary model name. Discovery and request admission both check supported capability; changing settings alone does not make an unsupported fast selector usable. Codex fast availability additionally depends on fresh account-specific model evidence, so it can be absent even when an override is enabled. Missing evidence does not establish entitlement. Fast mode and reasoning effort are separate choices, and neither promises a performance or price guarantee.

## JEV decision routing

JEV selects among your configured targets using a personal decision service or an ordinary personal route. It is a routing decision, not a general inference model or local command executor. Configure it in the selected organization:

1. Open Routing, create or edit a route, and select **JEV** as the routing strategy.
2. Under **JEV decision settings**, choose **Connection** or **Route alias** as the decision source.
3. For a connection, select your JEV or compatible custom decision-service connection. The server checks eligibility. **Decision service model** selects a custom SystemOne backend model; the canonical JEV service uses `jev-latest`.
4. For a route alias, select an ordinary personal route. The decision route cannot be this route or another JEV route; JEV chains and cycles are not supported. Resolve any saved connection or route shown as unavailable before relying on it.
5. Set **Decision timeout (ms)**. Zero selects the 4000 ms default; otherwise use an integer from 1000 to 120000.
6. Set **Session pin duration (seconds)**. Zero disables pinning; otherwise use an integer from 60 to 86400. A pin remains subject to current target eligibility and request compatibility.
7. Review the ordered targets and save. Saving replaces the complete route. Changing away from JEV removes its decision configuration; changing the decision-source type clears the previous source selection.

For each target, expand **Target reasoning and model profile**. Select the allowed reasoning efforts from `low`, `medium`, `high`, `xhigh`, `max`, and `ultra` as appropriate for that target. A checkbox does not establish that a provider/model supports that effort. **Model profile** is an optional description for decisions, limited to 512 characters. Do not include credentials or request content in it.

Choose a decision backend you authorize to receive bounded task, assistant, and tool-result text. The decision exchange does not send credentials or the entire conversation history. Its request and response are bounded to 64 KiB. A failed decision can fall back only to the first eligible configured target, so review target order deliberately. Cancellation and destination restrictions still apply. Automated tests do not establish live decision-service or provider acceptance.

### Advisory quota signals

Expand **Advisory quota signals** in JEV decision settings and select **Enable advisory quota signals** only if you want available quota information shared with the selected decision service. These signals are advisory: they do not authorize a request or replace current target eligibility checks. Missing quota stays unknown, not exhausted.

**Override quota thresholds** changes the used-quota tiers. All values are percentages of quota already used, from 0 to 100; each tier begins at its threshold. Empty Limited uses 70%, and empty Nearly exhausted uses 90%. Moderate is optional: empty means no moderate tier, while explicit zero means that tier starts at 0% used.

Require Moderate (if set) < Limited < Nearly exhausted. Values must be finite. Overrides alone do not enable signals, and configured thresholds must remain valid even when signals are off. These tiers are separate from native pool switching thresholds: sharing a quota tier with a decision service does not change a pool's switching policy.

## Retain readable reasoning during compaction

Reasoning retention is an optional per-route setting and is **off by default**. It applies to supported portable compaction; canonical ChatGPT compaction continues through its native forwarding behavior without this retention feature.

1. Open **Create route** or **Edit routing** and expand **Reasoning retention**.
2. Select **Configure reasoning retention** to show its settings. This alone does not enable retention.
3. Select **Retain readable reasoning during compaction** to opt in.
4. Set **Context limit (%)** and **Token limit**, or leave them empty for the defaults below.
5. Review the route and select **Save routing**.

An empty or zero context percentage uses **20%**. A nonzero percentage must be finite, greater than zero, and at most 100. An empty or zero token limit uses **100,000 tokens**; a nonzero value must be a whole number from 1 to 4,294,967,295. Configured limits must remain valid even when retention is disabled.

The inline retention budget is the smaller of the percentage of the selected compacting model's context window and the token limit. The context window comes from trusted, authenticated model metadata; there is no user-supplied context-window field. If that window is unknown, only the token limit applies. Setting either limit to zero selects its default, not unlimited retention and not disabled retention.

When enabled, readable reasoning is kept out of the summarizer and preserved as historical context in portable continuation. Overflow uses an encrypted temporary reference with a **24-hour lifetime**. It is not permanent history or an R2 media artifact, and it does not expose a server-local path. Temporary retention remains bounded: up to 256 entries and 512 MiB total per user within an organization, with at most 32 MiB per entry. A high token setting does not remove these storage bounds.

If required retained state is missing, expired, evicted, unavailable, or invalidated by credential replacement/deletion, continuation fails explicitly with `continuation_unavailable`; the failed continuation does not send upstream or silently omit the required context. If a required overflow write fails or exceeds its bounds, compaction stops before the upstream send. Start a new conversation explicitly when the required context cannot be recovered. Recovery from backup does not restore this temporary state.

To disable retention while keeping the numeric settings, clear **Retain readable reasoning during compaction** and save. To remove all retention settings, clear **Configure reasoning retention** and select **Save routing**. Saving replaces the complete route configuration; removing it clears the opt-in. Mocked console payload checks and separate controlled runtime tests do not establish live provider/account acceptance.

## Limits and continuation

Required default admission limits are 120 requests per minute and 10 concurrent requests per user per organization. Operators can choose 1–6,000 requests per minute and 1–100 concurrent requests; unlimited admission is not supported. Exhaustion returns HTTP 429 with `Retry-After` without an admission queue.

Temporary continuation can expire or be evicted. Responses state has a 24-hour lifetime with up to 1,000 entries and 1 GiB per user per organization. Tool-reasoning replay has a one-hour lifetime, 64 entries, and 256 KiB; bridged search replay has a one-hour lifetime, 64 entries, and 512 KiB. These examples are not a complete provider-state inventory.

Missing state must produce an explicit error, not a silent new conversation. Start a new conversation explicitly after expiry or recovery. Provider-specific timeouts also differ: local upstreams have no stall timeout by default unless configured. Check the release's path-specific behavior before tuning proxies or retrying slow work.

## Google Antigravity OAuth application

The instance operator must configure `TAIROUTER_GOOGLE_OAUTH_CLIENT_ID` and `TAIROUTER_GOOGLE_OAUTH_CLIENT_SECRET` with an authorized OAuth application compatible with the provider, requested scopes and callback. Login and token refresh are unavailable when either value is missing. No provider application credentials are bundled. Keep these values private; test the authorized integration before relying on it.
