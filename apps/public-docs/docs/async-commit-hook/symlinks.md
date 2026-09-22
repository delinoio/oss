# Committed symbolic links

On Windows, committed symbolic links are ordinary files containing their exact link target text, matching Git with `core.symlinks=false`. Targets are never dereferenced during preparation; Developer Mode and administrator privileges are not required. macOS and Linux retain native symbolic links. Checks that require native link behavior should select those platforms in their configuration.

Windows results from versions that prepared native links are incompatible with this representation. Submit `ach run --commit SHA` for fresh validation; old results remain available for inspection.
