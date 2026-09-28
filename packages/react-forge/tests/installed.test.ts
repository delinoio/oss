import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { cp, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import test from "node:test";
import { connect } from "./mcp/client.js";
import packageManifest from "../package.json" with { type: "json" };
const exec = promisify(execFile);
const magic = { pptx: "PK", docx: "PK", xlsx: "PK", pdf: "%PDF-", glb: "glTF", fbx: "Kaydara FBX Binary", sprite: "PK", wav: "RIFF" };
const cases: readonly (readonly [string, keyof typeof magic])[] = [["presentation", "pptx"], ["document", "docx"], ["workbook", "xlsx"], ["pdf", "pdf"], ["scene-glb", "glb"], ["scene-fbx", "fbx"], ["sprite", "sprite"], ["zombie-gunshot", "wav"], ...(process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? [] : [["animated-character-glb", "glb"], ["animated-character-fbx", "fbx"]] as const)];

test("scoped workspace archive installs and its CLI generates local formats and an offline Figma receipt", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge installed-"));
  const packageRoot = fileURLToPath(new URL("../", import.meta.url));
  const env = { ...process.env, PATH: `${dirname(process.execPath)}${delimiter}${process.env.PATH ?? ""}` };
  const pnpm = process.env.npm_execpath;
  assert.ok(pnpm, "Run installed consumer coverage through pnpm test");
  const pm = (args: string[], cwd: string) => exec(process.execPath, [pnpm, ...args], { cwd, env });
  try {
    await pm(["pack", "--pack-destination", directory], packageRoot);
    const tarball = join(directory, `delino-react-forge-${packageManifest.version}.tgz`);
    await writeFile(join(directory, "package.json"), JSON.stringify({ private: true, type: "module", packageManager: "pnpm@10.26.2", dependencies: { "@delino/react-forge": `file:${tarball}`, react: "19.2.8" } }));
    // Test the distributable layout without requiring lifecycle scripts,
    // workspace resolution or source files from this checkout.
    await pm(["install", "--ignore-scripts"], directory);
    await cp(join(packageRoot, "examples"), join(directory, "tasks"), { recursive: true });
    const installedRoot = join(directory, "node_modules", "@delino", "react-forge");
    const manifest = JSON.parse(await readFile(join(installedRoot, "package.json"), "utf8"));
    assert.equal(manifest.name, "@delino/react-forge");
    const cli = join(installedRoot, "bin", "react-forge.mjs");
    for (const [task, format] of cases) {
      const output = join(directory, `report-${task}.${format === "sprite" ? "sprite.zip" : format}`);
      const { stdout } = await exec(process.execPath, [cli, "run", join(directory, "tasks", `${task}.tsx`), "--output", output, "--json"], { cwd: directory, env });
      const result = JSON.parse(stdout); assert.equal(result.ok, true); assert.equal(result.format, format);
      const bytes = await readFile(output); assert.equal(bytes.subarray(0, magic[format].length).toString(), magic[format]);
    }
    for (const entry of ["./figma", "./glb", "./fbx", "./sprite"]) assert.ok(manifest.exports[entry]);
    const fake=await readFile(join(packageRoot,"tests","figma","fake.ts"),"utf8");
    await writeFile(join(directory,"tasks","fake-figma.ts"),fake.replace(/import[^;]+;/,"const CallSafety={Read:'read',Write:'write'};"));
    await writeFile(join(directory,"tasks","figma.tsx"),`import React from 'react';import {FigmaSession} from '@delino/react-forge';import {Page,Text} from '@delino/react-forge/figma';import {FakeConnection} from './fake-figma.js';export default async function(){const session=new FigmaSession({fileName:'Fixture',planKey:'team::1'},new FakeConnection());await session.render(<Page name="Installed"><Text>Editable</Text></Page>);return session;}`);
    const figmaOutput=join(directory,"result.figma.json");
    const figma=await exec(process.execPath,[cli,"run",join(directory,"tasks","figma.tsx"),"--output",figmaOutput,"--json"],{cwd:directory,env});
    assert.equal(JSON.parse(figma.stdout).status,"complete");
    const receipt=JSON.parse(await readFile(figmaOutput,"utf8"));assert.equal(receipt.format,"figma");assert.ok(receipt.createdNodeIds.length>=2);assert.ok(!JSON.stringify(receipt).includes("Editable"));
    const mcp = await connect(directory, cli);
    try {
      assert.equal((await mcp.client.listTools()).tools.length, 9);
      const syntax = await mcp.raw("execute", { code: "export default () => {\n const invalid = ;\n};" });
      assert.equal(syntax.isError, true);
      const syntaxError = syntax.structuredContent?.error as { code: string; diagnostics: { phase: string; source: string; line: number }[] };
      assert.equal(syntaxError.code, "malformed_input");
      assert.deepEqual({ phase: syntaxError.diagnostics[0]?.phase, source: syntaxError.diagnostics[0]?.source, line: syntaxError.diagnostics[0]?.line },
        { phase: "compile", source: "inline", line: 2 });
      const inline = await mcp.call("execute", { code: `import {createSession,Format} from '@delino/react-forge';
        import {Document,Page,Text} from '@delino/react-forge/pdf';
        export default async ({state})=>{const session=createSession(Format.Pdf);
          state.set('render',content=>session.render(<Document language="en-US"><Page>{content}</Page></Document>));
          await state.get('render')(<Text>INSTALLED_JSX_OK</Text>);return session;};` });
      const first = await mcp.call("inspect", { sessionId: inline.sessionId });
      assert.equal(first.targets.find((target: { kind: string; text?: string }) => target.kind === "paragraph")?.text, "INSTALLED_JSX_OK");
      await mcp.call("execute", { sessionId: inline.sessionId, code: `import {Text} from '@delino/react-forge/pdf';
        export default async ({state})=>{await state.get('render')(<Text>INSTALLED_JSX_UPDATED</Text>);};` });
      const updated = await mcp.call("inspect", { sessionId: inline.sessionId });
      assert.ok(updated.revision > first.revision);
      assert.equal(updated.targets.find((target: { kind: string; text?: string }) => target.kind === "paragraph")?.text, "INSTALLED_JSX_UPDATED");
      await mcp.call("close", { sessionId: inline.sessionId });
      for (const [task, format] of cases) {
        const created = await mcp.call("execute", { entry: `tasks/${task}.tsx` });
        const snapshot = await mcp.call("inspect", { sessionId: created.sessionId });
        assert.ok(snapshot.targets.length > 0);
        if (format === "glb" || format === "fbx") {
          const measured = await mcp.call("measure", { sessionId: created.sessionId, nodeId: snapshot.targets[0].nodeId, revision: snapshot.revision });
          assert.equal(measured.geometry.coordinateSpace, "world");
          if (task.startsWith("animated-character")) {
            const character = snapshot.targets.find((t: { name?: string }) => t.name === "Character");
            const wave = snapshot.targets.find((t: { name?: string }) => t.name === "wave");
            assert.ok(character && wave);
            const animated = await mcp.call("measure", { sessionId: created.sessionId, nodeId: character.nodeId,
              revision: snapshot.revision, animation: { clip: wave.nodeId, time: 0.5 } });
            assert.ok(animated.geometry.max[0] > measured.geometry.max[0] + 0.5);
            assert.equal(animated.revision, measured.revision);
          }
        }
        await mcp.call("export", { sessionId: created.sessionId, output: `mcp-${task}.${format === "sprite" ? "sprite.zip" : format}` });
        const bytes = await readFile(join(directory, `mcp-${task}.${format === "sprite" ? "sprite.zip" : format}`));
        assert.equal(bytes.subarray(0, magic[format].length).toString(), magic[format]);
        await mcp.call("close", { sessionId: created.sessionId });
      }
      const figma = await mcp.call("execute", { entry: "tasks/figma.tsx" });
      const published = await mcp.call("publish", { sessionId: figma.sessionId, receiptPath: "mcp.figma.json" });
      assert.equal(published.receipt.status, "complete");
      await mcp.call("close", { sessionId: figma.sessionId });
    } finally { await mcp.close(); }
    const shim = join(directory, "node_modules", ".bin", process.platform === "win32" ? "react-forge.cmd" : "react-forge");
    // cmd files require the Windows command processor. Only the fixture-owned
    // shim and a literal flag enter this command; document input never does.
    const version = process.platform === "win32"
      ? await exec(process.env.ComSpec ?? "cmd.exe", ["/d", "/s", "/c", `""${shim}" --version"`], { cwd: directory, env, windowsVerbatimArguments: true })
      : await exec(shim, ["--version"], { cwd: directory, env });
    assert.equal(version.stdout.trim(), packageManifest.version);
  } finally { await rm(directory, { recursive: true, force: true }); }
});
