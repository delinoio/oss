// Repository task orchestration only; application bundlers and test runners stay package-owned.
// Resolve the package-owned Vitest directly. Node 24 web storage must stay off
// so jsdom owns localStorage; remove the flag when Vitest handles Node globals.
// Explicit build inputs exclude read-after-write outputs and parent directory probes.
// Revisit automatic tracking when Vite Task can restore these multi-stage builds.
export default {
  "run": {
    "tasks": {
      "build": {
        "command": "node scripts/compile.mjs",
        "cache": true,
        "input": [
          "src/**",
          "public/**",
          "scripts/**",
          "*.json",
          "vite.config.ts",
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
            "pattern": "apps/devhud/src-tauri/icons/icon.png",
            "base": "workspace"
          }
        ],
        "output": [
          "dist/**",
          "build/**",
          "artifacts/**"
        ],
        "env": [
          "DEVHUD_CHROME_EXTENSION_ID",
          "DEVHUD_CHROME_EXTENSION_PUBLIC_KEY",
          "DEVHUD_EXTENSION_TEST_BUILD"
        ]
      },
      "build:test": {
        "command": "node scripts/build-test.mjs",
        "cache": true,
        "input": [
          "src/**",
          "public/**",
          "scripts/**",
          "*.json",
          "vite.config.ts",
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
            "pattern": "apps/devhud/src-tauri/icons/icon.png",
            "base": "workspace"
          }
        ],
        "output": [
          "dist/**",
          "build/**",
          "artifacts/**"
        ],
        "env": [
          "DEVHUD_CHROME_EXTENSION_ID",
          "DEVHUD_CHROME_EXTENSION_PUBLIC_KEY",
          "DEVHUD_EXTENSION_TEST_BUILD"
        ]
      },
      "build:frontend": {
        "command": "vp run build:test",
        "cache": true,
        "input": [
          "src/**",
          "public/**",
          "scripts/**",
          "*.json",
          "vite.config.ts",
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
            "pattern": "apps/devhud/src-tauri/icons/icon.png",
            "base": "workspace"
          }
        ],
        "output": [
          "dist/**",
          "build/**",
          "artifacts/**"
        ],
        "env": [
          "DEVHUD_CHROME_EXTENSION_ID",
          "DEVHUD_CHROME_EXTENSION_PUBLIC_KEY",
          "DEVHUD_EXTENSION_TEST_BUILD"
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
        "output": []
      },
      "lint": {
        "command": "vp run typecheck && node --test scripts/policy.test.mjs",
        "cache": false
      },
      "test:unit": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run",
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
        "output": []
      },
      "test:components": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run src/popup.test.ts src/popup-language.test.ts",
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
        "output": []
      },
      "test:accessibility": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run src/popup.test.ts src/popup-language.test.ts",
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
        "output": []
      },
      "test:package": {
        "command": "node --test scripts/policy.test.mjs scripts/deterministic-build.test.mjs",
        "cache": false
      },
      "test": {
        "command": "vp run lint && vp run test:unit && vp run test:accessibility && node --test scripts/deterministic-build.test.mjs",
        "cache": false
      }
    }
  }
};
