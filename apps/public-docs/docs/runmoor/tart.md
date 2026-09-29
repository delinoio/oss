# Runmoor Tart Images

> **Version note:** Runmoor 0.2.0 introduced automatic setup and managed runner updates. Guided creation of a new Mac VM during `init` and `image create --ipsw latest` are in the next release. Version 0.1.3 uses the explicit pinned configuration and manual image preparation also documented below. Check `runmoor version` before using release-specific commands.

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
