import net from "node:net";
import { commandInvocation } from "../../../scripts/dev-environment/process.mjs";
import { exitLikeChild, spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";

if (process.argv.length > 2) throw new Error("ach development has a fixed localhost:46308 binding; address overrides are not supported");

try {
  await new Promise((resolve, reject) => {
    const probe = net.createServer();
    probe.once("error", () => reject(new Error("Port 46308 is occupied. Stop the conflicting server before starting ach development.")));
    probe.listen(46308, "localhost", () => probe.close(resolve));
  });
  const pnpm = commandInvocation("pnpm");
  const result = await spawnDevServer(
    pnpm.command,
    [...pnpm.prefix, "exec", "rsbuild", "dev"],
    { stdio: "inherit", shell: false },
    { terminateProcessTree: true },
  );
  exitLikeChild(result);
} catch (error) {
  console.error(`ach development: ${error.message}`);
  process.exitCode = 1;
}
