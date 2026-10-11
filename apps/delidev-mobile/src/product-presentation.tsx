// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useState, type ReactNode } from "react";
import {
  decodeResourceDocument,
  type Resource,
} from "@delinoio/delidev-api-client";
import type { Labels } from "./localization";

/** One connection/view owns these numbers. Requests always use the original ID. */
export class PresentationLabels {
  private numbers = new Map<string, number>();
  private names = new Map<string, Set<string>>();
  private next = 0;
  identity(id: string, kind: string): string {
    if (!id) return "";
    let number = this.numbers.get(id);
    if (number === undefined) {
      number = ++this.next;
      this.numbers.set(id, number);
    }
    return `${kind} ${number}`;
  }
  observe(id: string, name: string) {
    if (!name) return;
    const ids = this.names.get(name) ?? new Set<string>();
    ids.add(id);
    this.names.set(name, ids);
  }
  observeResources(resources: readonly Resource[]) {
    for (const resource of resources) {
      const data = decodeResourceDocument(resource) ?? {};
      this.observe(
        resource.id,
        typeof data.name === "string" && data.name
          ? data.name
          : typeof data.alias === "string"
            ? data.alias
            : "",
      );
    }
  }
  name(id: string, name: string, kind: string, c: Labels): string {
    this.observe(id, name);
    const identity = this.identity(id, kind);
    if (!name) return `${identity} · ${c.nameUnavailable}`;
    return (this.names.get(name)?.size ?? 0) > 1
      ? `${name} · ${identity}`
      : name;
  }
}
const Context = createContext<PresentationLabels | undefined>(undefined);
export function PresentationLabelsProvider({
  children,
}: {
  children: ReactNode;
}) {
  const [labels] = useState(() => new PresentationLabels());
  return <Context.Provider value={labels}>{children}</Context.Provider>;
}
export function usePresentationLabels() {
  const owner = useContext(Context);
  const [local] = useState(() => new PresentationLabels());
  return owner ?? local;
}
