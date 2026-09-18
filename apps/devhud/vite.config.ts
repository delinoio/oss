// Repository task orchestration only; application bundlers and test runners stay package-owned.
// Resolve the package-owned Vitest directly. Node 24 web storage must stay off
// so jsdom owns localStorage; remove the flag when Vitest handles Node globals.
export default {
  "run": {
    "tasks": {
      "dev": {
        "command": "node scripts/development.mjs serve",
        "cache": false
      },
      "dev:frontend": {
        "command": "node ../../scripts/run-rsbuild-dev.mjs devhud 46305",
        "cache": false
      },
      "build": {
        "command": "node scripts/run-tauri.mjs build",
        "cache": false,
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "build:frontend": {
        "command": "rsbuild build",
        "cache": true,
        "input": [
          "src/**",
          "scripts/**",
          "*.json",
          "*.ts",
          "*.html",
          ".env",
          ".env.*",
          {
            "pattern": "package.json",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-lock.yaml",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-workspace.yaml",
            "base": "workspace"
          },
          {
            "pattern": ".nvmrc",
            "base": "workspace"
          },
          {
            "pattern": "vite.config.ts",
            "base": "workspace"
          },
          {
            "pattern": "packages/devhud-api-client/src/**",
            "base": "workspace"
          },
          {
            "pattern": "packages/devhud-api-client/package.json",
            "base": "workspace"
          },
          {
            "pattern": "packages/devhud-api-client/tsconfig*.json",
            "base": "workspace"
          },
          {
            "pattern": "packages/devhud-api-client/vite.config.ts",
            "base": "workspace"
          }
        ],
        "output": [
          "dist/**"
        ],
        "env": [
          "TAURI_ENV_PLATFORM"
        ],
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "typecheck": {
        "command": "tsc --noEmit",
        "cache": true,
        "input": [
          {
            "auto": true
          },
          {
            "pattern": "package.json",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-lock.yaml",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-workspace.yaml",
            "base": "workspace"
          },
          {
            "pattern": ".nvmrc",
            "base": "workspace"
          },
          {
            "pattern": "vite.config.ts",
            "base": "workspace"
          }
        ],
        "output": [],
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "mobile:generate": {
        "command": "node scripts/generate-mobile.mjs",
        "cache": false
      },
      "mobile:check": {
        "command": "node scripts/generate-mobile.mjs --check && node scripts/verify-mobile.mjs",
        "cache": false
      },
      "verify:mobile": {
        "command": "node scripts/verify-mobile.mjs",
        "cache": false
      },
      "dev:ios": {
        "command": "node scripts/run-mobile.mjs ios dev",
        "cache": false
      },
      "build:ios": {
        "command": "node scripts/run-mobile.mjs ios build --target aarch64",
        "cache": false
      },
      "build:ios:sim:arm64": {
        "command": "node scripts/run-mobile.mjs ios build --target aarch64-sim",
        "cache": false
      },
      "build:ios:sim:x64": {
        "command": "node scripts/run-mobile.mjs ios build --target x86_64",
        "cache": false
      },
      "dev:android": {
        "command": "node scripts/run-mobile.mjs android dev",
        "cache": false
      },
      "build:android": {
        "command": "vp run build:android:arm64 && vp run build:android:armv7",
        "cache": false
      },
      "build:android:arm64": {
        "command": "node scripts/run-mobile.mjs android build --target aarch64 --apk --aab",
        "cache": false
      },
      "build:android:armv7": {
        "command": "node scripts/run-mobile.mjs android build --target armv7 --apk --aab",
        "cache": false
      },
      "build:android:emulator:x64": {
        "command": "node scripts/run-mobile.mjs android build --target x86_64 --apk",
        "cache": false
      },
      "lint": {
        "command": "vp run typecheck && node --experimental-strip-types --test scripts/frontend-output-policy.test.mjs scripts/diagnostics-policy.test.mjs",
        "cache": false
      },
      "test:unit": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run --no-file-parallelism src/browser-context.test.ts src/deck.test.ts src/diagnostics.test.ts src/github-provider.test.ts src/identity-client.test.ts src/local-agent-contract.test.ts src/local-data.test.ts src/native-messaging.test.ts src/profile-secrets.test.ts src/realqa-submission.test.ts src/settings-contract.test.ts src/url-mapping.test.ts src/widget-contract.test.ts",
        "cache": true,
        "input": [
          {
            "auto": true
          },
          {
            "pattern": "package.json",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-lock.yaml",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-workspace.yaml",
            "base": "workspace"
          },
          {
            "pattern": ".nvmrc",
            "base": "workspace"
          },
          {
            "pattern": "vite.config.ts",
            "base": "workspace"
          }
        ],
        "output": [],
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "test:components": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run --no-file-parallelism src/App.test.tsx src/deck-ui.test.tsx src/github-settings-ui.test.tsx src/identity-settings.e2e.test.tsx src/identity-ui.test.tsx src/r2-settings-ui.test.tsx src/realqa-submission-ui.test.tsx src/realqa-ui.test.tsx src/surface-state.test.tsx src/ui-foundation.test.tsx src/updater-ui.test.tsx",
        "cache": true,
        "input": [
          {
            "auto": true
          },
          {
            "pattern": "package.json",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-lock.yaml",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-workspace.yaml",
            "base": "workspace"
          },
          {
            "pattern": ".nvmrc",
            "base": "workspace"
          },
          {
            "pattern": "vite.config.ts",
            "base": "workspace"
          }
        ],
        "output": [],
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "test:accessibility": {
        "command": "node --test scripts/accessibility.test.mjs scripts/localization.test.mjs",
        "cache": true,
        "input": [
          {
            "auto": true
          },
          {
            "pattern": "package.json",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-lock.yaml",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-workspace.yaml",
            "base": "workspace"
          },
          {
            "pattern": ".nvmrc",
            "base": "workspace"
          },
          {
            "pattern": "vite.config.ts",
            "base": "workspace"
          }
        ],
        "output": [],
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "test:security": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run --no-file-parallelism src/browser-context.test.ts src/diagnostics.test.ts src/identity-client.test.ts src/identity-settings.e2e.test.tsx src/local-data.test.ts src/profile-secrets.test.ts",
        "cache": true,
        "input": [
          {
            "auto": true
          },
          {
            "pattern": "package.json",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-lock.yaml",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-workspace.yaml",
            "base": "workspace"
          },
          {
            "pattern": ".nvmrc",
            "base": "workspace"
          },
          {
            "pattern": "vite.config.ts",
            "base": "workspace"
          }
        ],
        "output": [],
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "test:adapters": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run --no-file-parallelism src/github-provider.test.ts src/realqa-submission.test.ts src/r2-settings-ui.test.tsx src/local-agent-contract.test.ts",
        "cache": true,
        "input": [
          {
            "auto": true
          },
          {
            "pattern": "package.json",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-lock.yaml",
            "base": "workspace"
          },
          {
            "pattern": "pnpm-workspace.yaml",
            "base": "workspace"
          },
          {
            "pattern": ".nvmrc",
            "base": "workspace"
          },
          {
            "pattern": "vite.config.ts",
            "base": "workspace"
          }
        ],
        "output": [],
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "test:native": {
        "command": "cargo test --manifest-path ../../Cargo.toml -p devhud --all-targets",
        "cache": false
      },
      "test:native:capture": {
        "command": "cargo test --manifest-path ../../Cargo.toml -p devhud capture::tests",
        "cache": false
      },
      "test:native:shortcuts": {
        "command": "cargo test --manifest-path ../../Cargo.toml -p devhud shortcuts::tests",
        "cache": false
      },
      "test:native:ipc": {
        "command": "cargo test --manifest-path ../../Cargo.toml -p devhud native_messaging::tests && cargo test --manifest-path ../../Cargo.toml -p devhud-native-messaging-host",
        "cache": false
      },
      "test:native:updater": {
        "command": "cargo test --manifest-path ../../Cargo.toml -p devhud updater::tests",
        "cache": false
      },
      "test": {
        "command": "vp run lint && vp run test:unit && vp run test:components && vp run test:contracts",
        "cache": false
      },
      "verify:pins": {
        "command": "node scripts/verify-pins.mjs",
        "cache": false
      },
      "release:validate-updater": {
        "command": "node scripts/validate-updater-release.mjs",
        "cache": false
      },
      "smoke:platform": {
        "command": "node scripts/platform-smoke.mjs",
        "cache": false
      },
      "test:contracts": {
        "command": "node --experimental-strip-types --test scripts/localization.test.mjs scripts/registry.test.mjs scripts/accessibility.test.mjs scripts/native-bridge.test.mjs scripts/desktop-window.test.mjs scripts/mobile-policy.test.mjs scripts/generate-mobile.test.mjs scripts/development-csp.test.mjs scripts/frontend-output-policy.test.mjs scripts/platform-smoke-child.test.mjs scripts/run-tauri.test.mjs scripts/run-mobile.test.mjs scripts/stage-native-messaging-host.test.mjs scripts/verify-pins-policy.test.mjs scripts/diagnostics-policy.test.mjs scripts/validate-updater-release.test.mjs && node scripts/verify-frontend-output.mjs && vp run mobile:check",
        "cache": false
      }
    }
  }
};
