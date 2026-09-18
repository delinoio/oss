// Repository task orchestration only; application bundlers and test runners stay package-owned.
export default {
  "run": {
    "tasks": {
      "dev": {
        "command": "node ../../scripts/run-rspress-port.mjs public-docs dev 46302 -",
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
      "build:frontend": {
        "command": "vp run build",
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
        "command": "rspress preview",
        "cache": false
      },
      "test:routes": {
        "command": "node scripts/validate-clean-urls.mjs",
        "cache": false,
        "dependsOn": [
          "build:frontend"
        ]
      },
      "test": {
        "command": "vp run test:routes",
        "cache": false
      }
    }
  }
};
