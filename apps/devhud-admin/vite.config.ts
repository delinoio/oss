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
      "build": {
        "command": "rsbuild build",
        "cache": false,
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "build:frontend": {
        "command": "vp run build",
        "cache": false,
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "build:embedded": {
        "command": "node scripts/verify-build.mjs",
        "cache": false,
        "dependsOn": [
          "build"
        ]
      },
      "verify:embedded": {
        "command": "node scripts/verify-build.mjs",
        "cache": false,
        "dependsOn": [
          "build"
        ]
      },
      "typecheck": {
        "command": "tsc --noEmit",
        "cache": false,
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "lint": {
        "command": "vp run typecheck",
        "cache": false
      },
      "test:unit": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run src/api.test.ts src/auth.test.ts src/csp.test.ts",
        "cache": false,
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "test:components": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run src/App.test.tsx src/Dialog.test.tsx",
        "cache": false,
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "test:accessibility": {
        "command": "node --no-experimental-webstorage node_modules/vitest/vitest.mjs run src/App.test.tsx src/Dialog.test.tsx",
        "cache": false,
        "dependsOn": [
          "@delinoio/devhud-api-client#build"
        ]
      },
      "test": {
        "command": "vp run lint && vp run test:unit && vp run test:components && vp run test:accessibility && vp run verify:embedded && vp run test:port",
        "cache": false
      },
      "verify:build": {
        "command": "node scripts/verify-build.mjs",
        "cache": false
      },
      "test:port": {
        "command": "node scripts/port-collision.mjs",
        "cache": false
      }
    }
  }
};
