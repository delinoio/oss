# cmds-delidev-web-search-contract

## Scope

Backend slice of native Codex web search (issue #2014). It covers the frozen
search mode, strict decoding of native search items and their durable
publication as ordered Worker artifacts. The Settings UI, inherited defaults and
transcript disclosures depend on #1986/#1987 and are not part of this contract.

## Canonical Identifiers

```ts
enum WebSearchMode {
  Disabled = "disabled",
  Cached = "cached",
  Live = "live",
}

enum WebSearchActionKind {
  Search = "search",
  OpenPage = "openPage",
  FindInPage = "findInPage",
  Other = "other",
}
```

- Domain types: `cmds/delidev-cli/internal/domain/web_search.go`.
- Artifact kind: `web-search` (`ArtifactSnapshot.web_search`).
- Frozen field: `ExecutionJobInput.native_web_search_mode`; empty inherits native
  configuration and sends nothing.

## Contract

- A non-empty mode is sent to the native process as `-c web_search="<mode>"`.
  It is accepted only for the thread protocol, never for read-only Sidechat,
  the private title profile or model observation.
- A custom API binding keeps standalone search unavailable: only `disabled` is
  accepted there, `cached`/`live` fail as `Unsupported`. Unknown provider support
  is never assumed.
- The native `webSearch` item (`id`, `query`, optional `action`) is decoded
  strictly: unknown fields, unknown action types, duplicate keys and fields that
  do not belong to the action are incompatible. `action: null` is retained as
  kind `other`; no query, URL, result or citation is invented.
- A `webSearch` item that arrives while the mode is inherited, `disabled`, in
  Sidechat or on an API binding is not published (`Unsupported`). Disabled sends
  no search.
- Each observation retains the frozen mode actually in force. The Worker
  publishes started/completed artifacts in native order; the server rejects a
  `web-search` artifact whose mode differs from the frozen job input or whose
  mode is empty/disabled. There is no search sidecar or retransmission.

## Not Yet Implemented

- Worker capability and System/Worker protocol number allocation.
- Dispatch-time resolution of the mode from inherited defaults (#1986/#1987).
- Settings controls and transcript disclosures (Web search, Open page, Find text).
- Native/account acceptance; only synthetic fixtures were exercised.
