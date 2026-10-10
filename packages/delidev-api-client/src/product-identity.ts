// SPDX-License-Identifier: Apache-2.0
/** Presentation numbers are connection/view memory, never persistent aliases
 * or mutation authority. Callers keep the original ID in requests and proofs. */
export enum ProductIdentityKind {
  Resource = "resource", Project = "project", Repository = "repository",
  Account = "account", Provider = "provider", Agent = "agent", Session = "session",
  Backup = "backup", Connection = "connection", Server = "server", Device = "device",
  Worker = "worker", Execution = "execution", Request = "request", Schedule = "schedule",
  Notification = "notification", Operation = "operation", Reference = "reference", Input = "input", Snapshot = "snapshot", Template = "template",
}
export class ProductIdentityNumbers {
  private readonly identities = new Map<string, number>();
  number(_kind: ProductIdentityKind, originalId: string): number {
    const key = originalId;
    const retained = this.identities.get(key);
    if (retained !== undefined) return retained;
    const number = this.identities.size + 1;
    this.identities.set(key, number);
    return number;
  }
}
/** Apply only to DeliDev-generated diagnostic prose, never user names,
 * messages, terminal/model/tool output, or original external/native artifacts. */
export function productDiagnosticPresentation(value: string, reference: (id: string) => string): string {
  return value.replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi, reference);
}
