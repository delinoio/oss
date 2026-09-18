// Repository task orchestration only; application bundlers and test runners stay package-owned.
// Resolve the package-owned Vitest directly. Node 24 web storage must stay off
// so jsdom owns localStorage; remove the flag when Vitest handles Node globals.
export default {
  "run": {
    "tasks": {
      "build": {
        "command": "tsc -p tsconfig.build.json",
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
          }
        ],
        "output": [
          "dist/**"
        ]
      },
      "typecheck": {
        "command": "tsc -p tsconfig.json --noEmit",
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
        "command": "vp run typecheck",
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
      "test": {
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
      }
    }
  }
};
