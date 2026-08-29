import { appendFile } from "node:fs/promises";

const eventLog = process.env.DEVHUD_TEST_EVENT_LOG;
const forbidden = [
  "DEVHUD_DATABASE_URL",
  "DEVHUD_LOGTO_ISSUER",
  "DEVHUD_IDENTITY_HMAC_KEYS",
  "DEVHUD_INTERNAL_CONFIGURATION_COMPARISON_KEY",
  "DEVHUD_R2_SECRET_ACCESS_KEY",
  "DOCKER_HOST",
  "DOCKER_CONTEXT",
];
if (forbidden.some((name) => process.env[name])) {
  process.stderr.write("Turbo received a service configuration value\n");
  process.exitCode = 5;
} else if (!["team", "oss"].includes(process.env.DEVHUD_LOCAL_MODE)) {
  process.stderr.write("Turbo did not receive the bounded mode selector\n");
  process.exitCode = 6;
} else if (eventLog) {
  await appendFile(
    eventLog,
    `${JSON.stringify({ tool: "pnpm", action: "turbo", args: process.argv.slice(2), mode: process.env.DEVHUD_LOCAL_MODE })}\n`,
    "utf8",
  );
  if (process.env.DEVHUD_TEST_BLOCK_PNPM === "1") {
    await appendFile(
      eventLog,
      `${JSON.stringify({ tool: "pnpm", action: "turbo-blocked" })}\n`,
      "utf8",
    );
    await new Promise(() => setInterval(() => {}, 1_000));
  }
}
