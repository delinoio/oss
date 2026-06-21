# binpm

binpm is a Node-free binary package manager for installing and running command-line tools from release assets.

Use binpm when a tool already publishes native executables and you want project-local or user-level installation without npm, pnpm, yarn, Bun, Cargo install, Homebrew, or system package managers acting as the install backend.

## Start Here

- [Install binpm](/installation).
- [Declare and run a local tool](/getting-started).
- [Read the command overview](/commands).
- [Understand local manifests and lockfiles](/local-tooling).
- [Review cache and verification behavior](/cache-and-verification).

## Source Specs

Stable source identifiers are:

```text
github:owner/repo[@version]
github:<host>/owner/repo[@version]
gitlab:<host>/<namespace...>/<project>[@version]
```

Direct URLs, registries, and package-manager backends are not accepted source specs.
