# nodeup

Rustup-like Node.js version manager written in Rust.

## Install

Homebrew (recommended on macOS):

```bash
brew tap delinoio/tap https://github.com/delinoio/homebrew-tap
brew install nodeup
```

Curl + Bash bootstrap:

```bash
curl -fsSL https://raw.githubusercontent.com/delinoio/oss/main/scripts/nodeup/install.sh | bash
```

Linux package-manager install (artifact-backed):

```bash
curl -fsSL https://raw.githubusercontent.com/delinoio/oss/main/scripts/nodeup/install.sh | bash -s -- --method package --manager apt
```

## Uninstall

Clean uninstall (default includes `nodeup self uninstall`):

```bash
curl -fsSL https://raw.githubusercontent.com/delinoio/oss/main/scripts/nodeup/uninstall.sh | bash
```

Keep runtime/config/cache data:

```bash
curl -fsSL https://raw.githubusercontent.com/delinoio/oss/main/scripts/nodeup/uninstall.sh | bash -s -- --keep-data
```

## Troubleshooting

- Permission denied during package install:
: Re-run with elevated privileges or use `--method binary --prefix ~/.local`.
- PATH not updated in your shell:
: Re-run install without `--no-path-update`, or add `~/.local/bin` manually.
- Linux manager not detected:
: Pass `--manager <apt|dnf|yum|pacman|zypper>` explicitly, or use `--method binary`.
