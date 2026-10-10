// SPDX-License-Identifier: Apache-2.0
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { document, object } from "./documents";

export interface QuestionPresentation { revision: bigint; submitted: boolean; retired: boolean; state: string; closure: string }
/** A response is a server submission fact; only accepted or safely closed
 * original questions leave active presentation. Native delivery is separate. */
export function questionPresentation(row: Resource): QuestionPresentation | undefined {
  const data = document(row);
  if (row.kind !== EntityKind.INTERACTION || data.type !== "user-question") return undefined;
  const closure = data.closure;
  const knownClosure = closure === "open" || closure === "native-closed" || closure === "turn-ended";
  const response = object(data.response);
  const hasResponse = data.response != null;
  return { revision: row.revision, state: typeof response.state === "string" ? response.state : "", closure: typeof closure === "string" ? closure : "unknown", submitted: hasResponse || closure !== "open", retired: knownClosure && (hasResponse ? response.state === "accepted" : closure !== "open") };
}

export function questionReceiptMatches(row: Resource | undefined, request: object, sessionId: string): row is Resource {
  const mutation = (request as { mutation?: { id?: string; requestId?: string; expectedRevision?: bigint } }).mutation;
  if (!row || row.kind !== EntityKind.INTERACTION || row.sessionId !== sessionId || !mutation || typeof mutation.requestId !== "string" || !mutation.requestId || row.id !== mutation.id || typeof mutation.expectedRevision !== "bigint" || row.revision <= mutation.expectedRevision) return false;
  const data = document(row);
  return data.type === "user-question" && object(data.response).id === mutation.requestId;
}

export function projectActiveQuestion(row: Resource, retained: boolean, known?: QuestionPresentation) {
  const own = questionPresentation(row);
  const current = known && own && known.revision >= own.revision ? known : own;
  return { displayed: !current?.retired || retained, compact: Boolean(current?.submitted) };
}

/** Preserve the server page order and append only original final-page arrivals. */
export function activeRequestRows(base: readonly Resource[], live: ReadonlyMap<string, Resource>, removed: ReadonlySet<string>, arrivals: readonly string[], sessionId: string, lastPage: boolean): Resource[] {
  const originals = new Map<string, Resource>();
  for (const row of base) if (row.kind === EntityKind.INTERACTION && row.sessionId === sessionId && !removed.has(row.id)) {
    const previous = originals.get(row.id);
    if (!previous || row.revision >= previous.revision) originals.set(row.id, row);
  }
  const rows = [...originals.values()].map(row => {
    const update = live.get(row.id);
    return update?.kind === EntityKind.INTERACTION && update.sessionId === sessionId && update.revision > row.revision ? update : row;
  });
  if (!lastPage) return rows;
  const ids = new Set(rows.map(row => row.id));
  for (const id of arrivals) {
    const row = live.get(id);
    if (row?.kind === EntityKind.INTERACTION && row.sessionId === sessionId && !removed.has(id) && !ids.has(id)) { rows.push(row); ids.add(id); }
  }
  return rows;
}
