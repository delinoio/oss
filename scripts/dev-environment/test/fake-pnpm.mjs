import { appendFile } from "node:fs/promises";

const eventLog = process.env.DEVHUD_TEST_EVENT_LOG;
const forbidden = [
  "DEVHUD_DATABASE_URL",
  "DEVHUD_LOGTO_ISSUER",
  "DEVHUD_IDENTITY_HMAC_KEYS",
  "DEVHUD_R2_SECRET_ACCESS_KEY",
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
}
