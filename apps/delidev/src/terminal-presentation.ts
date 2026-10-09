// SPDX-License-Identifier: Apache-2.0
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document, object } from "./documents";

/** Connection-memory presentation tombstones never change server history. */
export class TerminalPresentation {
  private revisions = new Map<string, bigint>();
  private dismissed = new Set<string>();
  hidden(id: string) { return this.dismissed.has(id); }
  observe(sessionId: string, resource: Resource, unsettled = false): boolean {
    if (resource.kind !== EntityKind.TERMINAL || resource.sessionId !== sessionId || !resource.id || resource.schemaVersion !== 1 || resource.revision <= 0n) return false;
    const previous = this.revisions.get(resource.id) ?? 0n;
    if (resource.revision < previous || this.hidden(resource.id)) return false;
    this.revisions.set(resource.id, resource.revision);
    const data = document(resource);
    if (unsettled || data.pending != null && (typeof data.pending !== "object" || Array.isArray(data.pending)) || Object.keys(object(data.pending)).length || data.state !== "exited" || data.cleanup_verified !== true) return false;
    this.dismissed.add(resource.id);
    return true;
  }
}

export function terminalFallback(ids: readonly string[], removed: string): string {
  const at = ids.indexOf(removed);
  return ids.slice(0, Math.max(0, at)).at(-1) ?? ids.find(id => id !== removed) ?? "";
}
