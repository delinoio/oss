// Repository task orchestration only; application bundlers and test runners stay package-owned.
export default {
  "run": {
    "tasks": {
      "dev": {
        "command": "node ../../scripts/run-rspress-port.mjs nodeup-docs dev 46303 NODEUP_DOCS_DEV_PORT",
        "cache": false
      },
      "build": {
        "command": "rspress build",
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
          }
        ],
        "output": [
          "doc_build/**"
        ]
      },
      "preview": {
        "command": "node ../../scripts/run-rspress-port.mjs nodeup-docs preview 46251 NODEUP_DOCS_PREVIEW_PORT",
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
