// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useState, type ReactNode } from "react";
import {
  EntityKind,
  decodeResourceDocument,
  type Resource,
} from "@delinoio/delidev-api-client";
import { copy } from "./localization";

export type PresentationKind =
  | "server"
  | "agent"
  | "model"
  | "device"
  | "record"
  | "project"
  | "repository"
  | "session"
  | "account"
  | "provider"
  | "runner"
  | "terminal"
  | "backup"
  | "input";
const kinds: Partial<Record<EntityKind, PresentationKind>> = {
  [EntityKind.PROJECT]: "project",
  [EntityKind.REPOSITORY]: "repository",
  [EntityKind.SESSION]: "session",
  [EntityKind.ACCOUNT]: "account",
  [EntityKind.PROVIDER]: "provider",
  [EntityKind.MACHINE]: "runner",
  [EntityKind.TERMINAL]: "terminal",
  [EntityKind.QUEUE]: "input",
  [EntityKind.AGENT]: "agent",
  [EntityKind.MODEL]: "model",
  [EntityKind.DEVICE]: "device",
};
export function presentationKind(kind?: EntityKind): PresentationKind {
  return kind === undefined ? "record" : (kinds[kind] ?? "record");
}
const uuid = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;

/** Call only for DeliDev-owned diagnostics. User/native text must bypass this projection. */
export function diagnosticPresentation(value: unknown): string {
  return typeof value === "string"
    ? value.replace(uuid, copy("documents.internalReference"))
    : "";
}

/** Ephemeral display numbers never become IDs, request values or stored aliases. */
export class PresentationLabels {
  private numbers = new Map<string, number>();
  private names = new Map<string, Set<string>>();
  private next = new Map<PresentationKind, number>();
  number(value: string, kind: PresentationKind = "record"): number {
    const key = `${kind}:${value}`;
    let number = this.numbers.get(key);
    if (number === undefined) {
      number = (this.next.get(kind) ?? 0) + 1;
      this.next.set(kind, number);
      this.numbers.set(key, number);
    }
    return number;
  }
  identity(value: unknown, kind: PresentationKind = "record"): string {
    if (typeof value !== "string" || !value) return "";
    // External/native identifiers are retained verbatim by their owners. This
    // method receives only explicitly selected internal identity fields.
    return copy(`documents.kind.${kind}`, { number: this.number(value, kind) });
  }
  observe(value: string, name: string, kind: PresentationKind = "record") {
    this.number(value, kind);
    if (!name) return;
    const key = `${kind}:${name}`;
    const ids = this.names.get(key) ?? new Set<string>();
    ids.add(value);
    this.names.set(key, ids);
  }
  name(value: string, name: string, kind: PresentationKind = "record"): string {
    this.observe(value, name, kind);
    if (!name)
      return copy("documents.unavailableName", {
        kind: this.identity(value, kind),
      });
    return (this.names.get(`${kind}:${name}`)?.size ?? 0) > 1
      ? copy("documents.numberedName", {
          name,
          number: this.number(value, kind),
        })
      : name;
  }
  executionMetadata(value: Record<string, unknown>): Record<string, unknown> {
    const fields: Record<string, PresentationKind> = {
      job_id: "record",
      input_id: "input",
      execution_id: "record",
      session_id: "session",
      account_id: "account",
      provider_id: "provider",
      machine_id: "runner",
      request_id: "record",
    };
    // These keys belong to the DeliDev terminal observation envelope. Native
    // fields and arbitrary content are deliberately not recursively rewritten.
    return Object.fromEntries(
      Object.entries(value)
        .filter(([key]) => key !== "correlation_id")
        .map(([key, item]) => [
          key,
          Object.hasOwn(fields, key) ? this.identity(item, fields[key]) : item,
        ]),
    );
  }
  observeResources(resources: readonly Resource[]) {
    for (const resource of resources) {
      const data = decodeResourceDocument(resource) ?? {};
      const name =
        typeof data.name === "string" && data.name
          ? data.name
          : typeof data.alias === "string"
            ? data.alias
            : "";
      this.observe(resource.id, name, presentationKind(resource.kind));
    }
  }
  observeIds(ids: readonly string[], kind: PresentationKind) {
    for (const id of ids) this.number(id, kind);
  }
  resource(resource?: Resource): string {
    if (!resource) return copy("documents.extra.e504e6152194");
    const data = decodeResourceDocument(resource) ?? {};
    const name =
      typeof data.name === "string" && data.name
        ? data.name
        : typeof data.alias === "string"
          ? data.alias
          : "";
    return this.name(resource.id, name, presentationKind(resource.kind));
  }
}
const Context = createContext<PresentationLabels | undefined>(undefined);
export function PresentationLabelsProvider({
  children,
  labels,
}: {
  children: ReactNode;
  labels?: PresentationLabels;
}) {
  const [local] = useState(() => new PresentationLabels());
  return (
    <Context.Provider value={labels ?? local}>{children}</Context.Provider>
  );
}
export function usePresentationLabels(): PresentationLabels {
  const owner = useContext(Context);
  // Standalone dialogs and isolated component fixtures own their view lifetime.
  const [local] = useState(() => new PresentationLabels());
  return owner ?? local;
}
