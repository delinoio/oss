# DexDex Proto Contracts

`protos/dexdex/v1/*.proto` is the shared source of truth for DexDex Connect RPC contracts.
`protos/dexdex/v1/dexdex.proto` is a compatibility shim that imports all domain-specific files.

## Validation

```bash
cd protos/dexdex
buf lint
buf build
```

## Go Artifact Generation

```bash
cd protos/dexdex
buf generate
```

Generated Go artifacts are emitted under `protos/dexdex/gen` and are reproducible outputs.

## Breaking Compatibility Check

```bash
buf breaking protos/dexdex --against '.git#ref=HEAD~1,subdir=protos/dexdex'
```

`protos/dexdex/buf.yaml` uses package-level breaking comparison (`PACKAGE`) so domain file splits are allowed while preserving API compatibility.
