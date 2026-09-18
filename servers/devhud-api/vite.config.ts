// Repository task orchestration only; application bundlers and test runners stay package-owned.
export default {
  "run": {
    "tasks": {
      "dev": {
        "command": "node scripts/local.mjs serve",
        "cache": false
      },
      "migrate:local": {
        "command": "node scripts/local.mjs migrate",
        "cache": false
      },
      "ci:format": {
        "command": "node ../../scripts/ci/check-go-format.mjs servers/devhud-api",
        "cache": false
      },
      "ci:vet": {
        "command": "go vet ./...",
        "cache": false,
        "dependsOn": [
          "devhud-admin#build:embedded"
        ]
      },
      "ci:build": {
        "command": "go build ./cmd/devhud-api ./cmd/devhud-api-sweeper",
        "cache": false,
        "dependsOn": [
          "devhud-admin#build:embedded"
        ]
      },
      "ci:unit": {
        "command": "go test ./...",
        "cache": false,
        "dependsOn": [
          "devhud-admin#build:embedded"
        ]
      },
      "ci:migrations": {
        "command": "go test ./internal/postgres -run 'TestMigrate|TestNewPool|TestNewSweeperPool'",
        "cache": false,
        "dependsOn": [
          "devhud-admin#build:embedded"
        ]
      },
      "ci:integration": {
        "command": "go test -tags=integration ./internal/postgres",
        "cache": false,
        "dependsOn": [
          "devhud-admin#build:embedded"
        ]
      },
      "ci:api": {
        "command": "go test ./internal/auth ./internal/rpc ./internal/server ./internal/upload ./internal/updates",
        "cache": false,
        "dependsOn": [
          "devhud-admin#build:embedded"
        ]
      },
      "ci:sweeper": {
        "command": "go test ./cmd/devhud-api-sweeper ./internal/sweeper",
        "cache": false,
        "dependsOn": [
          "devhud-admin#build:embedded"
        ]
      }
    }
  }
};
