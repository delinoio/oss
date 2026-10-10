// SPDX-License-Identifier: Apache-2.0
import type { Resource } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { validRunnerObservation } from "./runner-observation";

/** Equal business revisions carry independent heartbeat observations. Reads
 * can change that projection, never the accepted configuration or request. */
export function mergeMachineRead(current: Resource | undefined, incoming: Resource | undefined, id: string): Resource | undefined {
  const exact = (row?: Resource): row is Resource => validRunnerObservation(row) && row.id === id;
  if (!exact(current)) return exact(incoming) ? incoming : undefined;
  if (!exact(incoming) || incoming.revision < current.revision) return current;
  if (incoming.revision > current.revision) return incoming;
  const fields = document(current), observed = document(incoming);
  if (fields.last_seen === observed.last_seen) return current;
  const projection = { ...fields };
  delete projection.last_seen;
  if (observed.last_seen !== undefined) projection.last_seen = observed.last_seen;
  return { ...current, documentJson: encode(projection) };
}
