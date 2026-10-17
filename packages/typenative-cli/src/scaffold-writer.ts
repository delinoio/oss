import { lstatSync, writeFileSync } from "node:fs";

// Relative writes use this child process's OS-pinned working directory. A rename
// or symlink replacement of its original path cannot redirect them elsewhere.
// Keep this isolated from the CLI process: changing its cwd would affect callers.
try {
  const identity = lstatSync(".", { bigint: true });
  if (
    !identity.isDirectory() ||
    String(identity.dev) !== process.argv[2] ||
    String(identity.ino) !== process.argv[3]
  )
    throw new Error("Directory changed");
  process.stdout.write("ready\n");
  let input = "";
  process.stdin.setEncoding("utf8");
  for await (const chunk of process.stdin) input += chunk;
  const files: Record<string, string> = JSON.parse(input);
  for (const [name, content] of Object.entries(files)) {
    if (
      !/^[a-zA-Z0-9_.-]+$/.test(name) ||
      name === "." ||
      name === ".." ||
      typeof content !== "string"
    )
      throw new Error("Invalid scaffold entry");
    writeFileSync(name, content, { flag: "wx" });
  }
} catch {
  // Parent emits the sanitized CLI diagnostic; never print project content.
  process.exitCode = 1;
}
