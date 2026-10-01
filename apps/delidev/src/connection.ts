import { createClient, type Transport } from "@connectrpc/connect";
import { clientFailure, FailureCode, SystemService } from "@delinoio/delidev-api-client";

export async function verifyLocalServer(transport: Transport, serverId: string) {
  const client = createClient(SystemService, transport);
  for (let attempt = 0; ; attempt++) {
    try {
      const status = await client.getStatus({}, { timeoutMs: 3000 });
      if (status.serverId !== serverId || status.protocolVersion !== 1 || status.version !== "0.1.0") throw "incompatible";
      if (status.stopping) throw "stopped";
      return;
    } catch (reason) {
      if (reason === "incompatible" || reason === "stopped") throw reason;
      const { code } = clientFailure(reason);
      console.warn("local_server_verification", { attempt: attempt + 1, code });
      if (attempt >= 2 || (code !== FailureCode.ServerUnavailable && code !== FailureCode.Unavailable)) throw reason;
      // Native readiness does not guarantee the renderer's first read succeeds.
      // Retry only this bounded status read, never startup, pairing or a product
      // mutation; identity and authorization failures remain terminal.
      await new Promise((resolve) => setTimeout(resolve, 250 * (attempt + 1)));
    }
  }
}
