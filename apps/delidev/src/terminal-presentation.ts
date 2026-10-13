// SPDX-License-Identifier: Apache-2.0
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document } from "./documents";

/**
 * Maximum container nesting accepted when canonicalizing pending evidence. An
 * authenticated document may nest far deeper within its size limit, so the
 * traversal is bounded to keep it from overflowing the call stack.
 */
const MAX_EVIDENCE_DEPTH = 32;

/**
 * Fixed fingerprint for evidence nested deeper than MAX_EVIDENCE_DEPTH. Such
 * evidence is never compared structurally and never dismisses: a deeply nested
 * pending value is present, and a deeply nested state or cleanup proof is not
 * the exact exited and verified evidence that dismissal requires.
 */
const OVER_DEPTH_FINGERPRINT = "over-depth";

/**
 * Canonical JSON with sorted object keys, so equal evidence compares equal.
 * Returns null, without recursing further, when containers nest deeper than
 * MAX_EVIDENCE_DEPTH.
 */
function canonical(value: unknown, depth = 0): string | null {
  if (Array.isArray(value)) {
    if (depth >= MAX_EVIDENCE_DEPTH) return null;
    const parts: string[] = [];
    for (const item of value) {
      const part = canonical(item, depth + 1);
      if (part === null) return null;
      parts.push(part);
    }
    return `[${parts.join(",")}]`;
  }
  if (value !== null && typeof value === "object") {
    if (depth >= MAX_EVIDENCE_DEPTH) return null;
    const parts: string[] = [];
    for (const key of Object.keys(value).sort()) {
      const part = canonical(
        (value as Record<string, unknown>)[key],
        depth + 1,
      );
      if (part === null) return null;
      parts.push(`${JSON.stringify(key)}:${part}`);
    }
    return `{${parts.join(",")}}`;
  }
  return JSON.stringify(value) ?? "null";
}

/** Compact non-cryptographic 53-bit digest; retains no document bytes. */
function digest(text: string): string {
  let h1 = 0xdeadbeef,
    h2 = 0x41c6ce57;
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    h1 = Math.imul(h1 ^ code, 2654435761);
    h2 = Math.imul(h2 ^ code, 1597334677);
  }
  h1 =
    Math.imul(h1 ^ (h1 >>> 16), 2246822507) ^
    Math.imul(h2 ^ (h2 >>> 13), 3266489909);
  h2 =
    Math.imul(h2 ^ (h2 >>> 16), 2246822507) ^
    Math.imul(h1 ^ (h1 >>> 13), 3266489909);
  return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(36);
}

/**
 * A pending operation exists for any present, non-null value, including an
 * empty object, a scalar or an array. Only a missing or null pending field
 * means no operation.
 */
function hasPending(pending: unknown): boolean {
  return pending !== undefined && pending !== null;
}

/**
 * Compact comparison evidence for one accepted revision: state, cleanup proof
 * and pending evidence. Equal revisions must carry identical evidence.
 */
function fingerprint(data: Record<string, unknown>): string {
  const evidence = canonical([
    data.state,
    data.cleanup_verified,
    hasPending(data.pending) ? data.pending : null,
    hasPending(data.pending),
  ]);
  return evidence === null ? OVER_DEPTH_FINGERPRINT : digest(evidence);
}

/** Stable categories for authenticated observations rejected as unsafe. */
export enum TerminalPresentationRejection {
  /** Same revision as the accepted observation, but different evidence. */
  RevisionContradiction = "revision-contradiction",
}

/** Connection-memory presentation tombstones never change server history. */
export class TerminalPresentation {
  private revisions = new Map<string, bigint>();
  /** One compact fingerprint per original ID for its latest accepted revision. */
  private fingerprints = new Map<string, string>();
  private dismissed = new Set<string>();
  private numbers = new Map<string, number>();
  hidden(id: string) {
    return this.dismissed.has(id);
  }
  /**
   * Returns the monotonic presentation number of an original terminal ID. The
   * number is allocated once, in first opened or authenticated-inventory
   * encounter order, and is never reused or released in this connection
   * lifetime, even after the terminal tab is closed or the payload is evicted.
   * It is presentation only; RPCs, receipts and cache keys keep the original ID.
   */
  number(id: string): number {
    let value = this.numbers.get(id);
    if (!value) {
      value = this.numbers.size + 1;
      this.numbers.set(id, value);
    }
    return value;
  }
  observe(sessionId: string, resource: Resource, unsettled = false): boolean {
    if (
      resource.kind !== EntityKind.TERMINAL ||
      resource.sessionId !== sessionId ||
      !resource.id ||
      resource.schemaVersion !== 1 ||
      resource.revision <= 0n
    )
      return false;
    this.number(resource.id);
    const previous = this.revisions.get(resource.id) ?? 0n;
    if (resource.revision < previous || this.hidden(resource.id)) return false;
    const data = document(resource);
    const current = fingerprint(data);
    if (
      resource.revision === previous &&
      this.fingerprints.get(resource.id) !== current
    ) {
      // Content-free diagnostic: distinguishes this integrity contradiction
      // from an ordinary running or unsettled terminal, which also returns false.
      console.warn({
        event: "terminal-presentation-observation-rejected",
        category: TerminalPresentationRejection.RevisionContradiction,
        terminalId: resource.id,
        revision: resource.revision.toString(),
      });
      return false;
    }
    this.revisions.set(resource.id, resource.revision);
    this.fingerprints.set(resource.id, current);
    if (
      unsettled ||
      hasPending(data.pending) ||
      data.state !== "exited" ||
      data.cleanup_verified !== true
    )
      return false;
    this.dismissed.add(resource.id);
    return true;
  }
}

export function terminalFallback(
  ids: readonly string[],
  removed: string,
): string {
  const at = ids.indexOf(removed);
  return (
    ids.slice(0, Math.max(0, at)).at(-1) ??
    ids.find((id) => id !== removed) ??
    ""
  );
}
