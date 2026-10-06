import { existsSync, globSync, readFileSync } from "node:fs";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));
const read = (path) => JSON.parse(readFileSync(`${root}/${path}`, "utf8"));
const rootTasks = read("turbo.json").tasks;
const workspaces = new Map(globSync(["apps/*/package.json", "packages/*/package.json", "servers/*/package.json", "scripts/ci/package.json"], { cwd: root }).map((path) => {
  const manifest = read(path);
  const config = `${dirname(path)}/turbo.json`;
  return [manifest.name, { manifest, tasks: existsSync(`${root}/${config}`) ? read(config).tasks : {} }];
}));

// Contract checks follow dependencies as well as leaf commands so a cached
// aggregate cannot silently lose a validation that used to live in Actions.
export function jobTaskGraph(job) {
  const graph = new Map();
  function visit(workspace, name) {
    const id = `${workspace}#${name}`;
    if (graph.has(id)) return;
    const owner = workspaces.get(workspace);
    if (!owner) throw new Error(`Unknown workspace ${workspace}`);
    const task = { ...(rootTasks[name] ?? {}), ...(owner.tasks[name] ?? {}) };
    graph.set(id, { command: owner.manifest.scripts[name] ?? "", task });
    for (const dep of task.dependsOn ?? []) {
      if (dep.startsWith("^")) {
        for (const [dependency, range] of Object.entries({ ...owner.manifest.dependencies, ...owner.manifest.devDependencies })) {
          if (range.startsWith("workspace:")) visit(dependency, dep.slice(1));
        }
      } else if (dep.includes("#")) visit(...dep.split("#"));
      else visit(workspace, dep);
    }
  }
  for (const { run } of job.steps) {
    const match = run?.match(/^node scripts\/ci\/run-affected\.mjs (\S+) (.+)$/u);
    if (match) for (const name of match[2].split(" ")) visit(match[1], name);
    const rust = run?.match(/^node scripts\/ci\/run-rust\.mjs (test|clippy)$/u);
    if (rust) {
      // Inventory includes every explicit selection variant. Runtime selects
      // exactly one after validating the central package list.
      for (const name of Object.keys(workspaces.get("@delinoio/ci").manifest.scripts)) {
        if (name === `ci:rust:${rust[1]}` || name.startsWith(`ci:rust:${rust[1]}:`)) visit("@delinoio/ci", name);
      }
    }
  }
  return graph;
}

export function jobCommands(job) {
  return [...job.steps.map(({ run }) => run ?? ""), ...[...jobTaskGraph(job)].map(([id, { command }]) => `${id}: ${command}`)].join("\n");
}
