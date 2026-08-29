import { appendFile } from "node:fs/promises";

const eventLog = process.env.DEVHUD_TEST_EVENT_LOG;
const action = process.argv.at(-1);
const required = [
  "DEVHUD_DATABASE_URL",
  "DEVHUD_PUBLIC_API_URL",
  "DEVHUD_LOGTO_AUDIENCE",
  "DEVHUD_IDENTITY_HMAC_KEYS",
];
if (required.some((name) => !process.env[name])) {
  process.stderr.write("fake Go did not receive its API allowlist\n");
  process.exitCode = 3;
} else if (process.env.DEVHUD_UNKNOWN_CANARY || process.env.DEVHUD_R2_ENDPOINT) {
  process.stderr.write("fake Go received an unvalidated name\n");
  process.exitCode = 4;
} else if (eventLog) {
  await appendFile(eventLog, `${JSON.stringify({ tool: "go", action })}\n`, "utf8");
}
