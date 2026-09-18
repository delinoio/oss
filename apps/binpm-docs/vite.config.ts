// Repository task orchestration only; application bundlers and test runners stay package-owned.
export default {
  "run": {
    "tasks": {
      "dev": {
        "command": "node ../../scripts/run-rspress-port.mjs binpm-docs dev 46304 -",
        "cache": false
      },
      "build": {
        "command": "rspress build && node scripts/copy-installers.mjs",
        "cache": true,
        "input": [
          "src/**",
          "scripts/**",
          "*.json",
          "*.ts",
          "*.html",
          ".env",
          ".env.*",
          "docs/**",
          "theme/**",
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
            "pattern": "scripts/install/binpm.*",
            "base": "workspace"
          }
        ],
        "output": [
          "doc_build/**"
        ]
      },
      "preview": {
        "command": "rspress preview --port 46261",
        "cache": false
      },
      "test": {
        "command": "node scripts/validate-clean-urls.mjs",
        "cache": false,
        "dependsOn": [
          "build"
        ]
      }
    }
  }
};
