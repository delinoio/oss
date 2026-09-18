# Runmoor Tart Images

Install **Tart 2.37.0** on macOS 14+ arm64 yourself. Install **Tart Guest Agent 0.14.2** with RPC enabled in the guest's non-root runner account. Account setup, login, Xcode licensing and tools remain manual. Review the version-specific [Tart license](https://github.com/openai/tart/blob/2.37.0/LICENSE) and [Guest Agent license](https://github.com/openai/tart-guest-agent/blob/v0.14.2/LICENSE), currently FSL-1.1-ALv2, plus the applicable Apple software terms. They are external software, not bundled or relicensed by Runmoor.

Image changes (`create`, `open`, `seal`, `remove`) require a running manager. Keep `runmoor run` or the user service running until setup and validation finish, so sleep inhibition covers a setup VM after `image open` returns. `image list` remains available offline. For the first image, start with a configuration containing only explicit host budgets and no pools or connections:

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
runmoor image create --name xcode --ipsw "$HOME/Downloads/restore.ipsw" --cpu 2 --memory-mib 4096
runmoor image create --name imported --from LOCAL_TART_NAME --cpu 2 --memory-mib 4096
runmoor image create --name imported-oci --from oci://REGISTRY/NAMESPACE/IMAGE:TAG --cpu 2 --memory-mib 4096
runmoor image list
runmoor image open --id IMAGE_UUID
runmoor image seal --id IMAGE_UUID --runner-version 2.337.0
```

Existing local sources must be stopped; `--source-home` selects an external Tart storage directory. Absolute `.tvm` exports and existing sealed revision UUIDs are also accepted by `--from`. OCI references resolve to immutable digests before sealing. Imports never modify the operator's original image.

Use a clean dedicated account, install the exact runner release, enable Guest Agent RPC for that logged-in account, and remove previous runner registration/credentials and nonempty workspaces before sealing. The guest runner account must be non-root. Keep personal credentials, keychains and SSH agents out of the base image. Sealing validates readiness/versions, stops the image, and publishes a local immutable revision.

A Tart pool uses `backend = "tart"`, `mode = "plain"`, `arch = "arm64"`, the sealed UUID as `image`, matching `runner_version` and `runner_path`, and explicit VM resources. Each job boots a new clone; the sealed base is never a job VM. Changing a base requires creating and sealing another revision. `image remove --id IMAGE_UUID` refuses active/referenced images. Close a setup window before removal. Setup and validation share the host budget and maximum **two concurrent Runmoor macOS VMs**.

Runmoor uses private Tart storage with automatic pruning disabled. It does not distribute macOS/Xcode images or retain failed job clones.
