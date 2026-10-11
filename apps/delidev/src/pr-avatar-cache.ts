// SPDX-License-Identifier: Apache-2.0
/** Connection-memory only. Blob URLs expose sanitized bytes, never origins. */
export class PRAvatarCache {
  private entries = new Map<string, string>();
  private attempts = new Set<string>();
  private listeners = new Set<() => void>();
  private version = 0;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  snapshot = () => this.version;
  private publish() { this.version++; for (const listener of this.listeners) listener(); }
  read(reference: string) { return this.entries.get(reference); }
  attempted(reference: string) { return this.attempts.has(reference); }
  put(reference: string, bytes?: Uint8Array) {
    if (this.entries.has(reference)) return;
    this.attempts.add(reference);
    // Attempt metadata is bounded independently; eviction does not immediately
    // refetch an invisible/failed row and churn the 64-raster cache.
    while (this.attempts.size > 128) this.attempts.delete(this.attempts.values().next().value!);
    if (bytes && bytes.length > 0 && bytes.length <= 128 << 10) {
      if (this.entries.size >= 64) { const oldest = this.entries.keys().next().value!; URL.revokeObjectURL(this.entries.get(oldest)!); this.entries.delete(oldest); }
      this.entries.set(reference, URL.createObjectURL(new Blob([new Uint8Array(bytes)], { type: "image/png" })));
    }
    this.publish();
  }
  fail(reference: string) { const url = this.entries.get(reference); if (url) URL.revokeObjectURL(url); this.entries.delete(reference); this.put(reference); }
  clear() { for (const url of this.entries.values()) URL.revokeObjectURL(url); this.entries.clear(); this.attempts.clear(); this.publish(); }
}
