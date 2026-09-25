// Test-only loopback viewer: serves an explicit artifact directory and a local
// Three.js bundle. No runtime package or remote resource dependency.
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";
const at = process.argv.indexOf("--input");
if (at < 0 || !process.argv[at + 1]) throw Error("Use --input <artifact directory>");
const input = resolve(process.argv[at + 1]);
const script = fileURLToPath(new URL("scene-viewer.js", import.meta.url));
const html = await readFile(new URL("scene-viewer.html", import.meta.url));
const bundle = await build({ entryPoints: [script], bundle: true, format: "esm", write: false });
const models = new Set(["studio", "headphones", "dac", "stand"].map(p => `/aura-${p}.glb`));
const renders = new Set(["studio", "headphones", "dac", "stand"].flatMap(p => ["glb", "fbx"].flatMap(f => ["hero", "front", "back", "detail"].map(v => `/renders/aura-${p}-${f}-${v}.png`))));
createServer(async (req, res) => {
  try {
    const path = new URL(req.url, "http://localhost").pathname;
    if (path === "/") { res.setHeader("Content-Type", "text/html"); res.end(html); }
    else if (path === "/viewer.js") { res.setHeader("Content-Type", "text/javascript"); res.end(bundle.outputFiles[0].contents); }
    else if (models.has(path)) { res.setHeader("Content-Type", "model/gltf-binary"); res.end(await readFile(join(input, path.slice(1)))); }
    else if (renders.has(path)) { res.setHeader("Content-Type", "image/png"); res.end(await readFile(join(input, path.slice(1)))); }
    else { res.statusCode = 404; res.end(); }
  } catch { res.statusCode = 500; res.end("Artifact unavailable"); }
}).listen(46319, "127.0.0.1", () => console.log("Scene verification viewer: http://127.0.0.1:46319"));
