import { readFile, readdir, stat } from "node:fs/promises";
import { relative, resolve, sep } from "node:path";

const dist = resolve("../../servers/devhud-api/internal/adminassets/dist");
const index = await readFile(resolve(dist, "index.html"), "utf8");

async function files(directory) {
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const target = resolve(directory, entry.name);
    if (entry.isDirectory()) result.push(...(await files(target)));
    else result.push(target);
  }
  return result;
}

const output = await files(dist);
const paths = output
  .map((file) => relative(dist, file).split(sep).join("/"))
  .toSorted();
const javascript = paths.filter((file) =>
  /^static\/js\/assets\/index\.[a-f0-9]{8}\.js$/u.test(file),
);
const styles = paths.filter((file) =>
  /^static\/css\/assets\/index\.[a-f0-9]{8}\.css$/u.test(file),
);
const licenses = paths.filter((file) =>
  /^static\/js\/assets\/index\.[a-f0-9]{8}\.js\.LICENSE\.txt$/u.test(file),
);
const expected = ["index.html", ...styles, ...javascript, ...licenses].toSorted();

if (paths.some((file) => file.endsWith(".map"))) {
  throw new Error("Production source maps must not be emitted.");
}
if (styles.length !== 1 || javascript.length !== 1 || licenses.length !== 1) {
  throw new Error(
    `Production output must contain one hashed CSS asset, one hashed JavaScript asset, and its license file; found ${JSON.stringify(paths)}.`,
  );
}
if (JSON.stringify(paths) !== JSON.stringify(expected)) {
  throw new Error(`Production output contains unexpected files: ${JSON.stringify(paths)}.`);
}
for (const file of output) {
  if ((await stat(file)).size === 0) {
    throw new Error(`Production output file is empty: ${relative(dist, file)}.`);
  }
}
for (const asset of [...styles, ...javascript]) {
  if (!index.includes(`/admin/${asset}`)) {
    throw new Error(`Production asset is not rooted at /admin/: ${asset}.`);
  }
}
