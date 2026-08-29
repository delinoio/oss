import { spawn } from "node:child_process";
import { acceptedMarker, rejectedMarker } from "../contracts.mjs";

const rejected = process.env.DEVHUD_TEST_LATE_PROVIDER_RESULT === "rejected";
const output = rejected
  ? `${rejectedMarker}${JSON.stringify({
      code: "environment.missing",
      message: "DevHud API configuration is missing required names",
      names: ["DEVHUD_DATABASE_URL"],
    })}\n`
  : `${acceptedMarker}\n`;
const stream = rejected ? "stderr" : "stdout";
if (!rejected) process.stderr.write("INFISICAL_WARNING_CANARY_MUST_NOT_LEAK\n");
if (process.platform === "win32") {
  process[stream].write(output);
} else {
  const writer = spawn(
    process.execPath,
    [
      "-e",
      `setTimeout(() => process.${stream}.write(${JSON.stringify(output)}), 50)`,
    ],
    { stdio: ["ignore", "inherit", "inherit"] },
  );
  writer.unref();
}
