# Clairic Client

## Goal

Provide a standalone Rust client for the Cloudflare Workers AI Clairic extraction model that can be published on crates.io.

## Project ID

`clairic-client`

## Domain Ownership Map

- `crates/clairic-client`: public Rust library, examples, tests, and package documentation.

## Domain Contract Documents

- [Rust client foundation](crates-clairic-client-foundation.md)

## Cross-Domain Invariants

The package uses Rust as explicitly requested. It has no repository-local runtime dependencies, generated assets, or service deployment. Public package documentation lives in its README and rustdoc; no public website is required. The stable upstream model ID is `@cf/cloudflare/clairic`.

## Change Policy

Update the domain contract for API or packaging changes and this index for ownership changes. Update scoped instructions only for development rules or ownership changes. Record validation evidence and unresolved acceptance limits in PRs or CI artifacts.

## References

- [Repository defaults](repository-defaults.md)
- [Cloudflare model reference](https://developers.cloudflare.com/workers-ai/models/clairic/)
