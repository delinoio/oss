import { spawnSync } from "node:child_process";

const [workspace, ...tasks] = process.argv.slice(2);
if (!workspace || tasks.length === 0 || tasks.some((task) => task.startsWith("-"))) throw new Error("Expected a workspace and task names");
const args = ["exec", "turbo", "run", ...tasks, "--filter", workspace];
if (process.env.FORCE_RUN !== "true") {
  for (const key of ["TURBO_SCM_BASE", "TURBO_SCM_HEAD"]) {
    if (!/^[0-9a-f]{40}$/u.test(process.env[key] ?? "")) throw new Error(`${key} must come from the CI comparison`);
  }
  args.push("--affected");
}
console.log(JSON.stringify({ event: "ci_workspace", workspace, tasks, forced: process.env.FORCE_RUN === "true" }));
const result = spawnSync("pnpm", args, { stdio: "inherit" });
if (result.error) throw result.error;
process.exitCode = result.status ?? 1;
