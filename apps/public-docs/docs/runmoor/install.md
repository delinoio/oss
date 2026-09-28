# Install and Verify Runmoor

On Ubuntu 22.04 or newer, use the [APT installation steps below](#linux-apt-and-dnf). Runmoor is distributed through the Delino repository, which you must register before installing with APT. The signed archive method is also available for Linux and Apple Silicon Macs.

## Homebrew on Apple Silicon Mac

On macOS 14 or newer with Apple Silicon, install from the Delino tap:

```sh
brew install delinoio/tap/runmoor
runmoor version
```

Homebrew installs the prebuilt release and checks its pinned SHA-256. Tart and runner images are installed separately. Installation does not configure runners or register or start a service. Intel Macs and Linux Homebrew are not supported.

To update an installation:

```sh
brew update
brew upgrade delinoio/tap/runmoor
```

To remove it, run `brew uninstall delinoio/tap/runmoor`. If you registered a Runmoor service, stop and uninstall that service before upgrading or removing the package; after upgrading, reinstall the service with the new executable and start it explicitly. See [Operations](./operations) for service commands. Your configuration and data remain yours.

## Release archives

Download the matching `runmoor-darwin-arm64.tar.gz`, `runmoor-linux-amd64.tar.gz` or `runmoor-linux-arm64.tar.gz` from the [`runmoor@v…` releases](https://github.com/delinoio/oss/releases). Download `SHA256SUMS` and the `.sigstore.json` bundles alongside it. A publication dry-run archive is unsigned and is not a public release.

Choose a published stable `runmoor@v…` release and replace `X.Y.Z` below with its version. Install cosign separately before verification. This example downloads and installs the Apple Silicon Mac archive; macOS 14 or newer is required.

```sh
(
set -eu
RUNMOOR_TAG='runmoor@vX.Y.Z'
RUNMOOR_ARCHIVE='runmoor-darwin-arm64.tar.gz'
RUNMOOR_IDENTITY="https://github.com/delinoio/oss/.github/workflows/release-runmoor.yml@refs/tags/${RUNMOOR_TAG}"
RUNMOOR_DOWNLOAD_DIR=$(mktemp -d)
trap 'rm -rf "$RUNMOOR_DOWNLOAD_DIR"' EXIT
cd "$RUNMOOR_DOWNLOAD_DIR"

for file in "$RUNMOOR_ARCHIVE" "${RUNMOOR_ARCHIVE}.sigstore.json" SHA256SUMS SHA256SUMS.sigstore.json; do
  curl --fail --location --silent --show-error \
    --output "$file" "https://github.com/delinoio/oss/releases/download/${RUNMOOR_TAG}/${file}"
done
cosign verify-blob \
  --bundle "${RUNMOOR_ARCHIVE}.sigstore.json" \
  --certificate-identity "$RUNMOOR_IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$RUNMOOR_ARCHIVE"
cosign verify-blob \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity "$RUNMOOR_IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
awk -v archive="$RUNMOOR_ARCHIVE" '$2 == archive { print; found++ } END { if (found != 1) exit 1 }' SHA256SUMS > SHA256SUMS.selected
shasum -a 256 -c SHA256SUMS.selected
tar -xzf "$RUNMOOR_ARCHIVE"
mkdir -p "$HOME/.local/bin"
install -m 755 runmoor "$HOME/.local/bin/runmoor"
"$HOME/.local/bin/runmoor" version
)
```

The download tag and signing identity must refer to the same release. Tag-triggered releases use the identity above. For a release explicitly signed by a manual run on `main`, use the exact identity `https://github.com/delinoio/oss/.github/workflows/release-runmoor.yml@refs/heads/main` after checking its release run.

Linux users select `runmoor-linux-amd64.tar.gz` or `runmoor-linux-arm64.tar.gz` and may use `sha256sum -c` instead of `shasum -a 256 -c`. Running the manager on Linux requires Ubuntu 22.04 or newer on x86-64 or ARM64. Add your user binary directory to PATH yourself. Direct archive installation does not register the executable with Homebrew or install an automatic updater, system-level service, or bundled Docker/Tart.

## Linux APT and DNF

Runmoor `0.1.3` is available as native APT and DNF packages through the Delino stable repository. The release archives above remain available as well. Installation does not register or start a service.

**Running the Runmoor manager on Linux requires Ubuntu 22.04 or newer**, on x86-64 or ARM64. Package installation and version/help commands have also been verified on Ubuntu 22.04/24.04/26.04 LTS, Debian 12/13, Fedora 43/44, and RHEL-compatible 9/10 systems, including UBI, Rocky Linux and AlmaLinux. The non-Ubuntu checks establish package compatibility only; the manager cannot run on those distributions.

The repository address is `https://pkgs.oss.delino.io`. Verify its public RSA 4096 key before registering it:

```sh
curl -fsSLo delino-packages.asc https://pkgs.oss.delino.io/keys/delino-packages.asc
gpg --show-keys --with-fingerprint delino-packages.asc
```

The full primary fingerprint must match `B08D E37A 14DD 10DD FD04 E66E 87CB 82A1 F70F BD30`. Stop on a mismatch.

### Ubuntu: register the repository, then install

**Downloading or inspecting the key does not register the repository.** Before running `apt-get install runmoor`, complete both [Verify the repository key](https://oss.delino.io/linux-packages#verify-the-repository-key) and [APT: stable](https://oss.delino.io/linux-packages#apt-stable) in the Linux package setup guide. Those steps install the verified keyring, register the Delino stable source, refresh APT's package lists, and install Runmoor.

If the repository is already registered, refresh its package list and check that APT can find a candidate before installing:

```sh
sudo apt-get update
apt-cache policy runmoor
```

The update must fetch the Delino `stable` repository without errors, and the policy output must show a candidate from `https://pkgs.oss.delino.io/apt`. Then install and verify:

```sh
sudo apt-get install runmoor
runmoor version
```

If APT reports `Unable to locate package runmoor`, its local package lists do not contain Runmoor for the selected architecture. Check the [APT troubleshooting steps](https://oss.delino.io/linux-packages#apt-cannot-find-runmoor) for missing registration, update errors, an incorrect suite, or an unsupported architecture.

### DNF: register the repository, then install

Complete the key verification and [DNF: stable setup](https://oss.delino.io/linux-packages#dnf-stable) first. These commands check package installation only; the manager requires Ubuntu:

```sh
sudo dnf install runmoor
runmoor version
```

### Update or remove an installed package

To update an existing APT installation:

```sh
sudo apt-get update
sudo apt-get install --only-upgrade runmoor
```

To update an existing DNF installation:

```sh
sudo dnf upgrade runmoor
```

Run the removal command only when you want to uninstall Runmoor. For APT:

```sh
sudo apt-get remove runmoor
```

For DNF:

```sh
sudo dnf remove runmoor
```

Use `runmoor version` to check the installed CLI. Package removal preserves user configuration and data.
