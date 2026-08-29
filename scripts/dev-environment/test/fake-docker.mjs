import { appendFile } from "node:fs/promises";

const args = process.argv.slice(2);
const eventLog = process.env.DEVHUD_TEST_EVENT_LOG;
const action = args.includes("version")
  ? "version"
  : args.includes("port")
    ? "port"
    : args.includes("up")
      ? "up"
      : args.includes("down")
        ? "down"
        : "unknown";

if (eventLog) {
  await appendFile(eventLog, `${JSON.stringify({ tool: "docker", action, args })}\n`, "utf8");
}

if (action === "version") process.stdout.write("Docker Compose version v2.40.0\n");
if (action === "port") process.exitCode = 1;
if (action === "up" && process.env.DEVHUD_TEST_DOCKER_UP_FAILURE === "1") process.exitCode = 1;
