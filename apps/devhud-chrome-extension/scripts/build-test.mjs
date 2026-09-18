// Fixture identity is restricted to validation builds; production requires the release identity.
process.env.DEVHUD_EXTENSION_TEST_BUILD = "1";
await import("./compile.mjs");
