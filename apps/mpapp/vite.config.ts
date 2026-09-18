// Repository task orchestration only; application bundlers and test runners stay package-owned.
export default {
  "run": {
    "tasks": {
      "start": {
        "command": "expo start",
        "cache": false
      },
      "android": {
        "command": "expo run:android",
        "cache": false
      },
      "ios": {
        "command": "expo run:ios",
        "cache": false
      },
      "test": {
        "command": "jest --config ./jest.config.ts --runInBand",
        "cache": false
      },
      "lint": {
        "command": "expo lint",
        "cache": false
      }
    }
  }
};
