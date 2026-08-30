import { appendFile } from "node:fs/promises";

const forbidden = [
  "DEVHUD_DATABASE_URL",
  "DEVHUD_IDENTITY_HMAC_KEYS",
  "DEVHUD_INTERNAL_CONFIGURATION_COMPARISON_KEY",
  "DEVHUD_INTERNAL_CONFIGURATION_COMPARISON_EXPECTED",
];
if (!process.env.DEVHUD_LOGTO_ISSUER) {
  process.stderr.write("fake frontend did not receive its issuer\n");
  process.exitCode = 3;
} else if (forbidden.some((name) => process.env[name])) {
  process.stderr.write("fake frontend received an unvalidated name\n");
  process.exitCode = 4;
} else if (process.env.DEVHUD_TEST_EVENT_LOG) {
  await appendFile(
    process.env.DEVHUD_TEST_EVENT_LOG,
    `${JSON.stringify({ tool: "frontend", action: "serve" })}\n`,
    "utf8",
  );
}
