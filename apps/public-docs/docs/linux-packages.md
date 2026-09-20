# Linux Packages

Use the signed Delino repository at `https://pkgs.oss.delino.io` to install CLI tools with APT or DNF.

## Supported systems

Packages support x86-64 and ARM64 on Ubuntu 22.04, 24.04 and 26.04 LTS; Debian 12 and 13; Fedora 43 and 44; and RHEL-compatible 9 and 10 systems, including UBI, Rocky Linux and AlmaLinux.

The stable repository contains `binpm`, `cargo-mono`, `nodeup`, `with-watch` and `derun`. Runmoor is available only from the separately enabled preview repository. Package versions use the original CLI version followed by `-1`. Older releases published before native package support are unavailable through these repositories.

Arch Linux, Alpine Linux, DevHud and TTL are outside this repository's support scope.

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
sudo install -d -m 0755 /etc/apt/keyrings
gpg --dearmor --output delino-packages.gpg delino-packages.asc
sudo install -m 0644 delino-packages.gpg /etc/apt/keyrings/delino-packages.gpg
curl -fsSLo delino.sources https://pkgs.oss.delino.io/setup/delino.sources
sudo install -m 0644 delino.sources /etc/apt/sources.list.d/delino.sources
sudo apt-get update
sudo apt-get install binpm
binpm --version
```

The source uses `Signed-By` to restrict this key to the Delino repository. Replace `binpm` with another stable package name as needed.

## DNF: stable

After verifying the same key:

```sh
sudo rpm --import delino-packages.asc
curl -fsSLo delino.repo https://pkgs.oss.delino.io/setup/delino.repo
sudo install -m 0644 delino.repo /etc/yum.repos.d/delino.repo
sudo dnf install binpm
binpm --version
```

The repository enables both `gpgcheck=1` and `repo_gpgcheck=1`. If DNF requests a key confirmation, compare the fingerprint above before accepting.

## Enable Runmoor preview

Preview is an explicit opt-in and remains enabled for later updates. After the key setup above, register the additional source for your package manager.

APT:

```sh
curl -fsSLo delino-preview.sources https://pkgs.oss.delino.io/setup/delino-preview.sources
sudo install -m 0644 delino-preview.sources /etc/apt/sources.list.d/delino-preview.sources
sudo apt-get update
sudo apt-get install runmoor
runmoor version
```

DNF:

```sh
curl -fsSLo delino-preview.repo https://pkgs.oss.delino.io/setup/delino-preview.repo
sudo install -m 0644 delino-preview.repo /etc/yum.repos.d/delino-preview.repo
sudo dnf install runmoor
runmoor version
```

Installation does not register or start a Runmoor service, configure runners, or install Docker or Tart. Follow the [Runmoor guide](https://runmoor.delino.io) for explicit setup and service commands. Package installation checks do not certify live GitHub or Tart integration.

## Update and remove

For APT, replace `binpm` with the installed package:

```sh
sudo apt-get update
sudo apt-get install --only-upgrade binpm
sudo apt-get remove binpm
```

For DNF:

```sh
sudo dnf upgrade binpm
sudo dnf remove binpm
```

Removing a CLI package preserves its user configuration and data. Stop and unregister a Runmoor service with the documented Runmoor commands before removing the executable.

To stop receiving updates, remove the corresponding `delino.sources` or `delino-preview.sources` from APT's sources directory, or `delino.repo` or `delino-preview.repo` from DNF's repository directory. Remove the Delino APT keyring only when neither Delino source remains registered.
