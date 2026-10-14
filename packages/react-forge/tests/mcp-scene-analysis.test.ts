import assert from "node:assert/strict";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { connect } from "./mcp/client.js";

const sceneTest =
  process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? test.skip : test;

sceneTest(
  "real SDK scene inspection pages one pinned analysis and rejects stale revisions",
  async () => {
    const cwd = await mkdtemp(join(tmpdir(), "react-forge-analysis-mcp-"));
    const peer = await connect(cwd);
    try {
      const { sessionId } = await peer.call("execute", {
        code: `import {createSession,Format} from '@delino/react-forge';
import {Scene,Mesh,Group} from '@delino/react-forge/glb';
export default async ({state}) => {
 const s=createSession(Format.Glb);
 const q=await s.registerGeometry({positions:new Float32Array([0,0,0,1,0,0,1,1,0,0,1,0]),normals:new Float32Array([0,0,1,0,0,1,0,0,1,0,0,1]),indices:new Uint32Array([0,1,2,0,2,3])});
 const t=await s.registerGeometry({positions:new Float32Array([0,0,0,1,0,0,0,1,0]),normals:new Float32Array([0,0,1,0,0,1,0,0,1]),indices:new Uint32Array([0,1,2])});
 await s.registerGeometry({positions:new Float32Array([0,0,0,2,0,0,2,1,0,0,1,0]),normals:new Float32Array([0,0,1,0,0,1,0,0,1,0,0,1]),indices:new Uint32Array([0,1,2,0,2,3])});
 state.set('q',q);state.set('t',t);
 await s.render(<Scene name="PRIVATE_ANALYSIS_NAME"><Group><Mesh geometry={q}/><Mesh geometry={q}/></Group><Mesh geometry={q}/><Mesh geometry={t}/></Scene>);
 return s;
};`,
      });
      const capabilities = await peer.call("capabilities");
      assert.equal(capabilities.formats.glb.sceneAnalysis, true);
      assert.equal((await peer.client.listTools()).tools.length, 9);
      const first = await peer.call("inspect", {
        sessionId,
        view: "scene_analysis",
        limit: 2,
      });
      const second = await peer.call("inspect", {
        sessionId,
        view: "scene_analysis",
        revision: first.revision,
        offset: 2,
        limit: 2,
      });
      assert.equal(first.meshCount, 4);
      assert.equal(first.referencedGeometryCount, 2);
      assert.equal(first.unusedGeometryCount, 1);
      assert.equal(first.registeredGeometryBytes, 372);
      assert.equal(first.referencedGeometryBytes, 236);
      assert.deepEqual(
        first.meshes.map((row: { triangleCount: number }) => row.triangleCount),
        [2, 2],
      );
      assert.deepEqual(
        second.meshes.map(
          (row: { triangleCount: number }) => row.triangleCount,
        ),
        [2, 1],
      );
      assert.equal(second.instanceTriangleCount, first.instanceTriangleCount);
      assert.equal(first.total, 4);
      assert.equal(first.nextOffset, 2);
      assert.equal(first.truncated, true);
      assert.equal(second.nextOffset, null);
      assert.equal(second.truncated, false);
      assert.equal(second.revision, first.revision);
      const qualityPage = await peer.call("inspect", {
        sessionId,
        view: "scene_analysis",
        geometryQuality: true,
        limit: 2,
      });
      assert.equal(qualityPage.geometryQuality.length, 1);
      assert.equal(
        qualityPage.meshes[0].geometryId,
        qualityPage.geometryQuality[0].geometryId,
      );
      assert.deepEqual(
        qualityPage.geometryQuality[0].findings.map(
          (finding: { count: number }) => finding.count,
        ),
        [0, 0, 0, 0, 0],
      );
      const qualityLast = await peer.call("inspect", {
        sessionId,
        view: "scene_analysis",
        geometryQuality: true,
        offset: 3,
        limit: 1,
      });
      assert.equal(qualityLast.geometryQuality.length, 1);
      assert.notEqual(
        qualityLast.geometryQuality[0].geometryId,
        qualityPage.geometryQuality[0].geometryId,
      );
      const wrongView = await peer.raw("inspect", {
        sessionId,
        geometryQuality: true,
      });
      assert.equal(
        (wrongView.structuredContent as any).error.code,
        "malformed_input",
      );
      const filtered = await peer.raw("inspect", {
        sessionId,
        view: "scene_analysis",
        nodeId: first.meshes[0].nodeId,
      });
      assert.equal(filtered.isError, true);
      assert.equal(
        (filtered.structuredContent as any).error.code,
        "malformed_input",
      );
      const targets = await peer.call("inspect", { sessionId });
      assert.equal(targets.revision, first.revision);
      await peer.call("execute", {
        sessionId,
        code: `import {Scene,Mesh} from '@delino/react-forge/glb';
export default async ({session,state})=>{await session.render(<Scene><Mesh geometry={state.get('t')}/></Scene>);};`,
      });
      const stale = await peer.raw("inspect", {
        sessionId,
        view: "scene_analysis",
        revision: first.revision,
      });
      assert.equal(stale.isError, true);
      assert.equal((stale.structuredContent as any).error.code, "conflict");
      assert.doesNotMatch(
        peer.stderr(),
        /PRIVATE_ANALYSIS_NAME|positions|0,1,2/,
      );
      await peer.call("close", { sessionId });
      const document = await peer.call("execute", {
        code: "import {createSession,Format} from '@delino/react-forge'; export default () => createSession(Format.Pdf);",
      });
      const unsupported = await peer.raw("inspect", {
        sessionId: document.sessionId,
        view: "scene_analysis",
      });
      assert.equal(unsupported.isError, true);
      assert.equal(
        (unsupported.structuredContent as any).error.code,
        "unsupported_edit",
      );
      await peer.call("close", { sessionId: document.sessionId });
    } finally {
      await peer.close();
      await rm(cwd, { recursive: true, force: true });
    }
  },
);

sceneTest(
  "real SDK geometry quality returns the portable category fixture",
  async () => {
    const cwd = await mkdtemp(join(tmpdir(), "react-forge-quality-mcp-"));
    const peer = await connect(cwd);
    try {
      const { sessionId } = await peer.call("execute", {
        code: `import {createSession,Format} from '@delino/react-forge';
import {Scene,Mesh} from '@delino/react-forge/glb';
export default async () => {
 const s=createSession(Format.Glb);
 const g=await s.registerGeometry({positions:new Float32Array([0,0,0,1,0,0,0,1,0,2,0,0,9,9,9]),normals:Float32Array.from({length:15},(_,i)=>i%3===2?1:0),indices:new Uint32Array([0,1,2,1,2,0,2,1,0,0,0,1,0,1,3])});
 await s.render(<Scene><Mesh geometry={g}/></Scene>);
 return s;
};`,
      });
      const report = await peer.call("inspect", {
        sessionId,
        view: "scene_analysis",
        geometryQuality: true,
      });
      assert.deepEqual(
        report.geometryQuality[0].findings.map(
          (finding: { count: number }) => finding.count,
        ),
        [1, 1, 2, 1, 1],
      );
      assert.deepEqual(
        report.geometryQuality[0].findings.map(
          (finding: { samples: number[] }) => finding.samples,
        ),
        [[4], [3], [3, 4], [1], [2]],
      );
      await peer.call("close", { sessionId });
    } finally {
      await peer.close();
      await rm(cwd, { recursive: true, force: true });
    }
  },
);
