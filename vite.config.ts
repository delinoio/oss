// Repository task orchestration only; application bundlers and test runners stay package-owned.
export default {
  "run": {
    // In 0.3.3 this is a kill switch, not a default overridden by task cache:true.
    // Keep it enabled and require an explicit cache policy for every task in CI.
    "cache": {
      "scripts": false,
      "tasks": true
    },
    "tasks": {
      "env:login": {
        "command": "node scripts/dev-environment/cli.mjs login",
        "cache": false
      },
      "env:doctor": {
        "command": "node scripts/dev-environment/cli.mjs doctor",
        "cache": false
      },
      "dev": {
        "command": "node scripts/dev-environment/cli.mjs start team",
        "cache": false
      },
      "dev:public-docs": {
        "command": "vp run public-docs#dev",
        "cache": false
      },
      "dev:nodeup-docs": {
        "command": "vp run nodeup-docs#dev",
        "cache": false
      },
      "dev:binpm-docs": {
        "command": "vp run binpm-docs#dev",
        "cache": false
      },
      "dev:oss": {
        "command": "node scripts/dev-environment/cli.mjs start oss",
        "cache": false
      },
      "dev:oss:down": {
        "command": "node scripts/dev-environment/cli.mjs down",
        "cache": false
      },
      "test:dev-environment": {
        "command": "node --test scripts/dev-environment/*.test.mjs",
        "cache": false
      },
      "ci:contracts": {
        "command": "node --test scripts/ci/*.test.mjs",
        "cache": false
      },
      "ci:workflows": {
        "command": "go tool actionlint",
        "cache": false
      },
      "ci:release-fixtures": {
        "command": "node --test scripts/release/*.test.mjs",
        "cache": false
      },
      "build": {
        "command": "vp run -r build",
        "cache": false
      },
      "test": {
        "command": "vp run -r test",
        "cache": false
      },
      "lint": {
        "command": "vp run -r lint",
        "cache": false
      },
      "proto:generate": {
        "command": "buf generate",
        "cache": true,
        "input": [
          "buf.gen.yaml",
          "buf.yaml",
          "protos/devhud/v1/**",
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
          "protos/gen/**",
          "packages/devhud-api-client/src/gen/**"
        ]
      },
      "proto:generate:cached": {
        "command": "vp run proto:generate",
        "cache": false
      },
      "proto:lint": {
        "command": "buf format --diff --exit-code && buf lint",
        "cache": false
      },
      "proto:breaking": {
        "command": "./scripts/check-proto-breaking.sh",
        "cache": false
      },
      "proto:fresh": {
        "command": "vp run --no-cache proto:generate && git diff --exit-code -- 'protos/gen' 'packages/devhud-api-client/src/gen' && test -z \"$(git ls-files --others --exclude-standard -- 'protos/gen' 'packages/devhud-api-client/src/gen')\"",
        "cache": false
      },
      "proto:check": {
        "command": "vp run proto:lint && vp run proto:breaking && vp run proto:fresh",
        "cache": false
      },
      "test:tasks": {
        "command": "node --test scripts/tasks/*.test.mjs",
        "cache": false
      }
    }
  }
};
