# Linux Packages

Use the signed Delino repository at `https://pkgs.oss.delino.io` to install CLI tools with APT or DNF.

## Supported systems

Packages support x86-64 and ARM64 on Ubuntu 22.04, 24.04 and 26.04 LTS; Debian 12 and 13; Fedora 43 and 44; and RHEL-compatible 9 and 10 systems, including UBI, Rocky Linux and AlmaLinux.

**Native packages are not published yet.** The commands below apply after each CLI’s first native package release. `binpm`, `cargo-mono`, `nodeup`, `with-watch`, `derun`, `runmoor` and `clibox` will all use stable. Preview is reserved and currently has no CLI packages. Package versions use the original CLI version followed by `-1`. Older releases published before native package support are unavailable through these repositories.

These APT and DNF repositories do not support Arch Linux or Alpine Linux and do not distribute DevHud.

## Verify the repository key

Install your distribution's `ca-certificates`, `curl` and `gnupg` packages first. Download the public key and inspect its full primary fingerprint:

```sh
curl -fsSLo delino-packages.asc https://pkgs.oss.delino.io/keys/delino-packages.asc
gpg --show-keys --with-fingerprint delino-packages.asc
```

The RSA 4096 primary key fingerprint must be exactly:

```text
B08D E37A 14DD 10DD FD04 E66E 87CB 82A1 F70F BD30
```

Stop if the fingerprint differs. Keep signature verification enabled when installing or updating packages.

## APT: stable

After verifying the key, install it in a dedicated keyring and register stable:

```sh
sudo install -d -m 0755 /usr/share/keyrings
gpg --dearmor --output delino-packages.gpg delino-packages.asc
sudo install -m 0644 delino-packages.gpg /usr/share/keyrings/delino-packages.gpg
sudo tee /etc/apt/sources.list.d/delino.sources <<'EOF'
Types: deb
URIs: https://pkgs.oss.delino.io/apt
Suites: stable
Components: main
Architectures: amd64 arm64
Signed-By: /usr/share/keyrings/delino-packages.gpg
EOF
sudo apt-get update
sudo apt-get install binpm
binpm --version
```

The source uses `Signed-By` to restrict this key to the Delino repository. Replace `binpm` with any available stable CLI package from the list above. APT also installs `delino-archive-keyring`, which keeps this repository’s public certificate current through authenticated package updates.

## DNF: stable

After verifying the same key:

```sh
sudo rpm --import delino-packages.asc
sudo tee /etc/yum.repos.d/delino.repo <<'EOF'
[delino]
name=Delino stable
mirrorlist=https://pkgs.oss.delino.io/rpm/stable/$basearch/mirrorlist
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://pkgs.oss.delino.io/keys/delino-packages.asc
metadata_expire=300
sslverify=1
EOF
sudo dnf install binpm
binpm --version
```

The repository enables both `gpgcheck=1` and `repo_gpgcheck=1`. If DNF requests a key confirmation, compare the fingerprint above before accepting.

## Preview registration

Preview currently has no CLI packages. Runmoor uses stable; it does not require this additional repository.

Preview is an explicit opt-in and remains enabled for later updates. After the key setup above, register the additional source for your package manager.

APT:

```sh
sudo tee /etc/apt/sources.list.d/delino-preview.sources <<'EOF'
Types: deb
URIs: https://pkgs.oss.delino.io/apt
Suites: preview
Components: main
Architectures: amd64 arm64
Signed-By: /usr/share/keyrings/delino-packages.gpg
EOF
sudo apt-get update

```

DNF:

```sh
sudo tee /etc/yum.repos.d/delino-preview.repo <<'EOF'
[delino-preview]
name=Delino preview
mirrorlist=https://pkgs.oss.delino.io/rpm/preview/$basearch/mirrorlist
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://pkgs.oss.delino.io/keys/delino-packages.asc
metadata_expire=300
sslverify=1
EOF
sudo dnf makecache
```

## Runmoor and clibox

After their first native releases, use the stable registration above and install `runmoor` or `clibox` with APT or DNF. Check `runmoor version` or `clibox --version`. Native clibox installation does not require Node.js; desktop helpers remain optional user-installed tools.

Installation does not register or start a Runmoor service, configure runners, or install Docker or Tart. Follow the [Runmoor guide](https://oss.delino.io/runmoor/) for explicit setup and service commands. Package installation checks do not certify live GitHub or Tart integration.

## Update and remove

For APT, replace `binpm` with the installed package:

```sh
sudo apt-get update
sudo apt-get install --only-upgrade delino-archive-keyring binpm
sudo apt-get remove binpm
```

For DNF:

```sh
sudo dnf upgrade binpm
sudo dnf remove binpm
```

Removing a CLI package preserves its user configuration and data. Stop and unregister a Runmoor service with the documented Runmoor commands before removing the executable.

To stop receiving updates, remove the corresponding `delino.sources` or `delino-preview.sources` from APT's sources directory, or `delino.repo` or `delino-preview.repo` from DNF's repository directory. Remove the Delino APT keyring only when neither Delino source remains registered.

Keep `delino-archive-keyring` updated before a signing key expires. If a machine has been offline through a key change and APT cannot authenticate the repository, repeat the public-key download, full fingerprint check and keyring installation above, then run `apt-get update`. Never disable signature verification to recover.
