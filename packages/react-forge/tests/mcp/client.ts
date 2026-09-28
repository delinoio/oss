import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { CallToolResultSchema, type CallToolResult } from "@modelcontextprotocol/sdk/types.js";

export async function connect(cwd: string, cli = fileURLToPath(new URL("../../bin/react-forge.mjs", import.meta.url))) {
  const transport = new StdioClientTransport({ command: process.execPath, args: [cli, "mcp", "--cwd", cwd], stderr: "pipe", env: Object.fromEntries(Object.entries(process.env).filter((entry): entry is [string, string] => entry[1] !== undefined)) });
  // The SDK may finish close() after its termination deadline but before the
  // child emits close. Windows still locks the fixture directory in that gap.
  const processClosed = new Promise<void>(resolve => { transport.onclose = resolve; });
  let stderr = "";
  transport.stderr?.on("data", chunk => { stderr += String(chunk); });
  const client = new Client({ name: "react-forge-test", version: "1" });
  client.onerror = () => {};
  await client.connect(transport);
  return {
    client, transport, stderr: () => stderr,
    async raw(name: string, args: Record<string, unknown> = {}, signal?: AbortSignal): Promise<CallToolResult> {
      return client.callTool({ name: `react_forge_${name}`, arguments: args }, CallToolResultSchema, { signal, timeout: 60_000 }) as Promise<CallToolResult>;
    },
    async call(name: string, args: Record<string, unknown> = {}) {
      const result = await this.raw(name, args);
      assert.ok(!result.isError, JSON.stringify(result));
      assert.deepEqual(JSON.parse((result.content[0] as { text: string }).text), result.structuredContent);
      return result.structuredContent as Record<string, any>;
    },
    async close() {
      await client.close();
      let timeout: ReturnType<typeof setTimeout> | undefined;
      try {
        await Promise.race([
          processClosed,
          new Promise<never>((_, reject) => {
            timeout = setTimeout(() => reject(new Error("MCP server did not close after client shutdown")), 10_000);
          }),
        ]);
      } finally { if (timeout) clearTimeout(timeout); }
    },
  };
}
