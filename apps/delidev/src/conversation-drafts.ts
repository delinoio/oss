// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";

/** User-authored edits survive payload eviction. This stores no Resource,
 * credentials, native observation or authoritative mutation receipt. */
export function useConversationDrafts<T>() {
  const [values, setValues] = useState<ReadonlyMap<string, T>>(new Map());
  const save = (id: string, value?: T) => setValues(current => {
    const next = new Map(current);
    if (value === undefined) next.delete(id); else next.set(id, value);
    return next;
  });
  return { values, save };
}
