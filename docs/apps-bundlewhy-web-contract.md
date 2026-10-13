# bundlewhy web UI contract

## Scope

`apps/bundlewhy-web` owns the React/TypeScript UI for live, saved and exported bundlewhy reports.

## Runtime and Language

React 19 and TypeScript, built with Rsbuild into one self-contained `dist/index.html` with inlined scripts and styles and no external resources. Server state uses React Query through `@connectrpc/connect-query`. The document is embedded in the Rust binary and in exported reports; it is not deployed to Cloudflare Pages (a documented deviation in [the project index](project-bundlewhy.md)).

## Users and Operators

Application developers on desktop browsers. English only. Mobile-specific layouts are out of scope.

## Interfaces and Contracts

- Live and saved mode: the page reads the access token from the `#token=` fragment, keeps it in session storage, removes it from the address bar and sends it as a bearer token to the same-origin Connect server.
- Exported mode: the Rust exporter replaces the `<!--bundlewhy:report-->` marker with a `<script type="application/json" id="bundlewhy-report">` element containing `{ analysis, sources }` in proto3 JSON. The UI validates it with the generated schemas and serves the same service from an in-memory transport. Invalid data shows `BW_RESULT_INVALID`; an unknown schema version shows `BW_SCHEMA_UNSUPPORTED`.
- Layout: header with project, engine identity, run status, Open report and Export HTML; a persistent diagnostic-engine notice with exact engine, adapter and override details; left category filters with counts and coverage state; center searchable diagnostic list with Confirmed/Inferred/Unavailable filters and a chunk size view; right diagnostic details with condition outcomes, location, snippet, full source, evidence, dependency paths and corrective direction.
- Three columns at widths of at least 1280px; narrower windows switch between the list and the details.
- States: loading progress with cancellation, empty results, no coverage, unavailable analysis, partial or cancelled or failed runs, problems with codes and recovery instructions, and invalid results. "No findings in covered categories" is distinct from "no analysis coverage".
- Sizes show raw, gzip and Brotli bytes per chunk and mapped source bytes only where attribution is supported. The UI never shows estimated or potential savings.
- Accessibility: keyboard operation, visible focus, labelled controls, status labels with text and icons rather than color alone, live regions for progress, and modal dialogs that trap focus, close with Esc and return focus.

## Storage

Session storage holds only the access token for the current tab. No other persistence.

## Security

All report content renders as React text; the UI uses no raw HTML insertion and renders no links from report content. The exported document performs no network requests.

## Logging

No telemetry. Errors are shown in the UI with codes.

## Build and Test

`pnpm --filter bundlewhy-web test` runs type checking, Vitest component tests, the production build and an output test that rejects external resources. Remove `dist/` after local validation.

## Dependencies and Integrations

- [Connect protocol](protos-bundlewhy-v1-contract.md)
- [Rust foundation](crates-bundlewhy-foundation.md)

## Change Triggers

Layout, state or transport changes update this contract and public interpretation docs when user-visible.

## References

- [Project index](project-bundlewhy.md)
- [Repository defaults](repository-defaults.md)
