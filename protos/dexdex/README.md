# DexDex Proto Contracts

`protos/dexdex/v1/dexdex.proto` is the shared source of truth for DexDex Connect RPC contracts.

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
