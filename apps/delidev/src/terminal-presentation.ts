// SPDX-License-Identifier: Apache-2.0
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document } from "./documents";

/** Compact non-cryptographic 53-bit digest; retains no document bytes. */
class Digest {
  private h1 = 0xdeadbeef;
  private h2 = 0x41c6ce57;
  update(text: string) {
    for (let i = 0; i < text.length; i++) {
      const code = text.charCodeAt(i);
      this.h1 = Math.imul(this.h1 ^ code, 2654435761);
      this.h2 = Math.imul(this.h2 ^ code, 1597334677);
    }
  }
  finish(): string {
    const h1 =
      Math.imul(this.h1 ^ (this.h1 >>> 16), 2246822507) ^
      Math.imul(this.h2 ^ (this.h2 >>> 13), 3266489909);
    const h2 =
      Math.imul(this.h2 ^ (this.h2 >>> 16), 2246822507) ^
      Math.imul(h1 ^ (h1 >>> 13), 3266489909);
    return (4294967296 * (2097151 & h2) + (h1 >>> 0)).toString(36);
  }
}

/**
 * Streams the canonical JSON of a value, with sorted object keys so equal
 * evidence compares equal, into a digest. The traversal uses an explicit work
 * list instead of recursion, so an authenticated document may nest as deeply
 * as its size limit allows without overflowing the call stack, and every
 * distinct document keeps its own evidence-specific fingerprint.
 */
function canonicalDigest(root: unknown): string {
  const digest = new Digest();
  const work: unknown[] = [{ value: root }];
  // Entries are either literal text to emit or a boxed value to expand.
  while (work.length > 0) {
    const entry = work.pop();
    if (typeof entry === "string") {
      digest.update(entry);
      continue;
    }
    const value = (entry as { value: unknown }).value;
    if (Array.isArray(value)) {
      digest.update("[");
      work.push("]");
      for (let i = value.length - 1; i >= 0; i--) {
        work.push({ value: value[i] });
        if (i > 0) work.push(",");
      }
    } else if (value !== null && typeof value === "object") {
      digest.update("{");
      work.push("}");
      const keys = Object.keys(value).sort();
      for (let i = keys.length - 1; i >= 0; i--) {
        work.push({ value: (value as Record<string, unknown>)[keys[i]] });
        work.push(`${JSON.stringify(keys[i])}:`);
        if (i > 0) work.push(",");
      }
    } else {
      digest.update(JSON.stringify(value) ?? "null");
    }
  }
  return digest.finish();
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
  return canonicalDigest([
    data.state,
    data.cleanup_verified,
    hasPending(data.pending) ? data.pending : null,
    hasPending(data.pending),
  ]);
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
