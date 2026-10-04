# Runmoor Tart Images

> **Version note:** Runmoor 0.2.0 introduced automatic setup and managed runner updates. Runmoor 0.2.3 supports guided creation of a new Mac VM during `init` and `image create --ipsw latest`. Version 0.1.3 uses the explicit pinned configuration and manual image preparation also documented below. Check `runmoor version` before using release-specific commands.

Install a **stable Tart 2.x.x release** on macOS 14+ arm64 yourself. Runmoor builds with this compatibility update accept a complete SemVer `2.MINOR.PATCH` triplet with optional build metadata; prereleases are unsupported. Previously installed binaries retain their original version check until upgraded.

Install **Tart Guest Agent 0.14.2** with RPC enabled in the guest's non-root runner account. Account setup, login, Xcode licensing and tools remain manual.

Review the license at your selected [Tart release](https://github.com/openai/tart/releases) (for example, the [2.37.0 license](https://github.com/openai/tart/blob/2.37.0/LICENSE)) and the [Guest Agent 0.14.2 license](https://github.com/openai/tart-guest-agent/blob/v0.14.2/LICENSE), plus the applicable Apple software terms. The linked 2.37.0 and 0.14.2 sources use FSL-1.1-ALv2; other Tart releases may have different terms. They are external software, not bundled or relicensed by Runmoor.

## Create a Mac image during first setup

Run `runmoor init` in a terminal and choose `tart`, then `create` when asked
for the image. The default `latest` asks Tart to download the newest Apple
restore image supported by your Mac; an absolute local `.ipsw` path also works.
Runmoor starts a temporary image-only manager, creates and opens the VM, and
keeps supervising it while you complete macOS setup.

In the VM, prepare a non-root `runner` login account, install **Guest Agent
0.14.2** so `--run-agent` starts with that account after boot, then install any
tools and accept any required Xcode license. A normal Homebrew install may
select a different Guest Agent version; check the version in the guest. Keep
personal credentials, previous runner registration and job workspaces out of
the image. Press Enter in the original terminal when the VM is ready. Runmoor
checks Guest Agent RPC, restarts the VM to check it again without manual
intervention, installs the verified latest GitHub runner, seals the image and
creates the final configuration. Start the manager with `runmoor run`.

If setup is interrupted, run `runmoor init` again with the same `--config`
path. Runmoor resumes its owned image rather than downloading another IPSW.
It never replaces an existing final configuration or an image with uncertain
ownership. The existing `--image`, `--image-source` and `--image-only`
paths remain available; noninteractive init still requires an existing image.

## Install Guest Agent 0.14.2 in the VM

Run these steps **inside the macOS VM**, logged in as the non-root `runner`
account. Installing Runmoor and Tart on the host does not install Guest Agent
inside the VM. Enter confirms that your manual setup is finished; Runmoor also
needs a working Guest Agent connection before it can continue.

First check `tart-guest-agent --version` in the VM. Runmoor requires **0.14.2**;
do not assume that the current Homebrew formula installs that version. If an
agent is already running, inspect its version and login configuration before
replacing it or starting another copy.

The [0.14.2 source](https://github.com/openai/tart-guest-agent/tree/v0.14.2)
can be built from commit `0540136b95fcafac66f2c9a507178ae62502919b` when a
matching release binary is unavailable. With Go and the Apple command-line
tools installed in the guest, run:

```sh
set -eu
umask 077
agent_build_dir=$(mktemp -d)
curl --fail --location --proto '=https' --proto-redir '=https' \
  'https://github.com/openai/tart-guest-agent/archive/0540136b95fcafac66f2c9a507178ae62502919b.tar.gz' \
  -o "$agent_build_dir/source.tar.gz"
mkdir "$agent_build_dir/source"
tar -xzf "$agent_build_dir/source.tar.gz" --strip-components=1 \
  -C "$agent_build_dir/source"
mkdir -p "$HOME/.local/bin"
(
  cd "$agent_build_dir/source"
  go build -trimpath \
    -ldflags '-X github.com/cirruslabs/tart-guest-agent/internal/version.Version=0.14.2 -X github.com/cirruslabs/tart-guest-agent/internal/version.Commit=0540136b95fcafac66f2c9a507178ae62502919b' \
    -o "$HOME/.local/bin/tart-guest-agent" ./cmd
)
"$HOME/.local/bin/tart-guest-agent" --version
```

Keep the downloaded source and its license with this local build. The version
output must start with `0.14.2`; this build includes its source commit suffix.

Install a login agent for the same account. The example below creates a new
definition exclusively. If that file already exists, inspect it instead of
overwriting it. Its PATH makes the installed binary available to commands
executed through Guest Agent.

```sh
set -eu
umask 077
mkdir -p "$HOME/Library/LaunchAgents"
agent_plist="$HOME/Library/LaunchAgents/io.delino.runmoor.tart-guest-agent.plist"
(
  set -C
  cat > "$agent_plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>io.delino.runmoor.tart-guest-agent</string>
  <key>ProgramArguments</key><array>
    <string>AGENT_BINARY</string><string>--run-agent</string>
  </array>
  <key>EnvironmentVariables</key><dict>
    <key>PATH</key><string>AGENT_PATH</string>
  </dict>
  <key>WorkingDirectory</key><string>AGENT_HOME</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
PLIST
)
plutil -remove ProgramArguments.0 "$agent_plist"
plutil -insert ProgramArguments.0 -string "$HOME/.local/bin/tart-guest-agent" "$agent_plist"
plutil -replace EnvironmentVariables.PATH -string "$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin" "$agent_plist"
plutil -replace WorkingDirectory -string "$HOME" "$agent_plist"
plutil -lint "$agent_plist"
launchctl bootstrap "gui/$(id -u)" "$agent_plist"
launchctl print "gui/$(id -u)/io.delino.runmoor.tart-guest-agent"
```

[`--run-agent`](https://github.com/openai/tart-guest-agent/tree/v0.14.2)
enables RPC. `--run-daemon` alone does not enable RPC, and running RPC as root
does not meet Runmoor's non-root account requirement.

The account must also log in after the VM restarts so its login agent starts
during Runmoor's headless boot check. In the VM's System Settings, select
**Users & Groups → Automatically log in as → runner**. FileVault or login
policies can prevent automatic login; see [Apple's automatic login
guide](https://support.apple.com/en-us/102316). Configure the guest's login
policy before confirming setup.

## Recover setup that waits after Enter

Keep the original setup terminal open while you check Guest Agent in the VM.
On Runmoor 0.2.3, `MANAGER_UNAVAILABLE: Cannot contact the local manager` can
also mean that the Guest Agent check exceeded its deadline. That message alone
does not prove that the manager stopped or that its socket needs removal.
Preserve the VM and setup files.

After correcting Guest Agent, wait for Runmoor to confirm readiness. If the
setup process has exited or you interrupted it, run `runmoor init` again with
the same `--config` path and the same storage environment. Do not start a second
setup process while the first one still owns the VM. Resume repeats the boot
check and seals only after your confirmation and successful guest validation.

For a pending setup created by 0.2.3, correct Guest Agent and resume with the
same Runmoor version first. Newer builds require an embedded VM ownership
identity that older setups may lack. If an upgraded binary returns
`OWNERSHIP_AMBIGUOUS`, preserve the original VM and records and follow
[Tart ownership recovery](#tart-ownership-recovery). Do not add or rewrite an
ownership marker to make the upgrade accept an older VM.

Builds with the setup diagnostic fix report guest readiness failures separately
from request timeouts and cancellations, and end the wizard if its setup
manager exits. A rejected guest version or image after confirmation also ends
setup while preserving the owned image for correction and retry.

## Managed runners from a prepared Mac image

Once an operator-owned image has a clean non-root account and the required Guest
Agent RPC, provide it directly to init:

```sh
runmoor init --backend tart \
  --target https://github.com/OWNER/REPOSITORY \
  --credential-env RUNMOOR_PAT \
  --image-source LOCAL_TART_NAME
runmoor run
```

Use `--source-home PATH` for an external Tart home, or select a `.tvm` export,
`oci://` reference or existing sealed UUID. The corresponding TOML setting is
`image_source = { from = "LOCAL_TART_NAME" }`, optionally with `source_home`.
It cannot be combined with `image`. Runmoor imports and freezes a separate base,
then installs the verified latest runner in a clone, validates it and seals the
result. Failed installations and future runner updates reuse that fixed base.
Source tags are not
followed for OS or Xcode upgrades; select a new source explicitly for those.

With an existing sealed UUID, use `image = "IMAGE_UUID"` and omit
`runner_version`. For a fixed environment, specify the image's exact installed
version instead. CPU, memory and architecture can be omitted in either mode.
The dedicated runner directory must not contain personal files, credentials or
previous job workspaces. Automatic installation replaces that directory only in
a preparation image, never in a running job or the original imported source.

For a new Mac image that still needs interactive setup, `runmoor init
--image-only` creates an empty manager configuration with automatic host budgets.
Use the manual preparation workflow below. `image create` can omit `--cpu` and
`--memory-mib`; `image seal --id IMAGE_UUID` installs the latest runner by default.
Keep `--runner-version VERSION` to validate a manually installed exact version.

## Manual image preparation

Image changes (`create`, `open`, `seal`, `remove`) require a running manager. Keep `runmoor run` or the user service running until setup and validation finish, so sleep inhibition covers a setup VM after `image open` returns. `image list` remains available offline. For the first image, start with a configuration containing no pools or connections. `schema_version = 1` alone uses automatic budgets; the following explicit example is also supported:

```toml
schema_version = 1

[host]
max_runners = 2
cpu = 4
memory_mib = 8192
min_free_disk_mib = 20480
```

Choose budgets appropriate for your Mac, start `runmoor run`, and issue image commands from a second terminal using the same configuration. After sealing, add the connection and Tart pool, then run `runmoor reload`.

```sh
runmoor image create --name xcode --ipsw latest --cpu 2 --memory-mib 4096
runmoor image create --name imported --from LOCAL_TART_NAME --cpu 2 --memory-mib 4096
runmoor image create --name imported-oci --from oci://REGISTRY/NAMESPACE/IMAGE:TAG --cpu 2 --memory-mib 4096
runmoor image list
runmoor image open --id IMAGE_UUID
runmoor image seal --id IMAGE_UUID --runner-version 2.337.0
```

Existing local sources must be stopped; `--source-home` selects an external Tart storage directory. Absolute `.tvm` exports and existing sealed revision UUIDs are also accepted by `--from`. OCI references resolve to immutable digests before sealing. Imports never modify the operator's original image.

For manual pinning, use a clean dedicated account, install the exact runner release, enable Guest Agent RPC for that logged-in account, and remove previous runner registration/credentials and nonempty workspaces before sealing. The guest runner account must be non-root. Keep personal credentials, keychains and SSH agents out of the base image. Sealing validates readiness/versions, stops the image, and publishes a local immutable revision.

Runmoor Tart pools support macOS guests only. The Tart image metadata must report the guest OS as `darwin`; Linux guests and images with missing or unrecognized OS metadata return `IMAGE_INVALID` before sealing can boot or reserve the VM. Runmoor also checks this value before accepting or cloning an existing sealed base. If an older unsupported revision appears in `image list`, it remains available for diagnosis and can be explicitly removed once no pool or execution references it. Prepare and seal a supported macOS image, then update the pool to use its UUID.

A Tart pool uses `backend = "tart"`, `mode = "plain"`, `arch = "arm64"`, the sealed UUID as `image`, matching `runner_version` and `runner_path`, and optional VM resource overrides. Each job boots a new clone; the sealed base is never a job VM. Changing a base requires creating and sealing another revision. `image remove --id IMAGE_UUID` refuses active/referenced images. Close a setup window before removal. Setup and validation share the host budget and maximum **two concurrent Runmoor macOS VMs**.

`image seal --runner-path` and TOML `runner_path` must use the same absolute guest directory, beginning with `/` and containing no `..`, NUL or line breaks. Paths are preserved exactly; resolve parent-directory traversal before sealing. The default Tart location is the `actions-runner` directory inside the runner account's home.

Runmoor uses private Tart storage with Tart automatic pruning disabled. Runmoor separately collects unreferenced revisions that it generated for runner updates; user-created revisions require explicit removal. It does not distribute macOS/Xcode images or retain failed job clones.

## Tart ownership recovery

Runmoor verifies that a Tart VM still belongs to the same Runmoor image or
execution before inspecting or changing it. If that identity is missing or no
longer matches, Runmoor reports `OWNERSHIP_AMBIGUOUS` and preserves the VM,
Runmoor records and capacity reservation. `runmoor stop --force` does not
override this check.

Stop the manager and preserve its complete state and data together. If you have
a paired backup containing the matching VM and Runmoor identity, restore both
from that backup, then start Runmoor and inspect `runmoor status` and
`runmoor doctor`. Never edit ownership files or delete a VM just because its
name matches an old record.

Older installations and interrupted VM creation may lack matching identity
proof. Runmoor will not adopt those VMs by name. Keep that installation intact.
If you can identify the original operator-owned image source, create a separate
Runmoor configuration with new absolute `[storage].state` and `[storage].data`
locations, then import that source as a new image revision. The new installation
uses a fresh identity; the uncertain VM and its records remain preserved in
the old storage.
