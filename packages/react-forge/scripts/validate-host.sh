#!/usr/bin/env bash
set -euo pipefail

target="${1:?Rust target is required}"
: "${RUNNER_TEMP:?RUNNER_TEMP is required}"

REACT_FORGE_TARGET="$target" node --input-type=module -e '
  import { readFileSync } from "node:fs";
  const hosts = JSON.parse(readFileSync("packages/react-forge/src/native-platforms.json", "utf8"));
  const host = hosts.find(({ target }) => target === process.env.REACT_FORGE_TARGET);
  if (!host || host.platform !== process.platform || host.architecture !== process.arch || process.versions.node.split(".")[0] !== "24") {
    throw new Error("React Forge runner does not match the selected native target");
  }
'

cargo test --locked --target "$target" -p react-forge-node -p forge-package -p forge-document -p forge-docx -p forge-xlsx -p forge-pdf -p forge-sprite -p forge-sfx -p forge-figma -p forge-tree-doc -p forge-pptx -p delino-forge
cargo test --locked --target "$target" -p forge-document -p forge-pdf -- --include-ignored
cargo clippy --locked --target "$target" -p react-forge-node -p forge-package -p forge-document -p forge-docx -p forge-xlsx -p forge-pdf -p forge-sprite -p forge-sfx -p forge-figma --all-targets -- -D warnings
export REACT_FORGE_SKIP_SCENE_TESTS=1
pnpm exec turbo run build typecheck lint test --filter=@delino/react-forge 2>&1 | tee "$RUNNER_TEMP/react-forge-turbo.log"
pnpm --filter @delino/react-forge typecheck:examples

if [[ "$RUNNER_OS" == "Windows" ]]; then
  REACT_FORGE_WINDOWS_CONSOLE_TEST=1 cargo test --locked --target "$target" -p react-forge-node --test windows_console -- --nocapture
fi

pnpm --filter @delino/react-forge cli run examples/travel-ir.tsx --output "$RUNNER_TEMP/react-forge-travel-ir.pptx" --json
node packages/react-forge/scripts/package.mjs main
node packages/react-forge/scripts/package.mjs native
node packages/react-forge/scripts/install-smoke.mjs

if [[ "$RUNNER_OS" != "Windows" ]]; then
  REACT_FORGE_PYTHON="$RUNNER_TEMP/react-forge-python/bin/python" pnpm --filter @delino/react-forge test:render --output "$RUNNER_TEMP/react-forge-render"
fi
pnpm --filter @delino/react-forge benchmark --output "$RUNNER_TEMP/react-forge-benchmark.json"
