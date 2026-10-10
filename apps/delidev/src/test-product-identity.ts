// SPDX-License-Identifier: Apache-2.0
/** Assert the new product name and its unchanged private DOM identity together.
 * This matcher avoids depending on traversal order of the scoped number map. */
export function productName(original: string) {
  const ids = original.match(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi) ?? [];
  let pattern = original.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  for (const id of ids) pattern = pattern.replaceAll(id, "(?:Project|Repository|Backup|Resource|Account|프로젝트|저장소|백업|리소스|계정) \\d+");
  // Off-catalog projects no longer carry a UUID-derived visible fallback.
  pattern = pattern.replace("Project · (?:Project|", "(?:Project|");
  const expected = new RegExp(`^${pattern}$`);
  return (name: string, element: Element | null) => {
    if (!expected.test(name) || ids.some(id => name.includes(id))) return false;
    const owner = element?.closest("[data-project-id], [data-repository-id], [data-backup-id]");
    return !ids.length || Boolean(owner && ids.every(id => ["data-project-id", "data-repository-id", "data-backup-id"].some(attribute => owner.getAttribute(attribute) === id)));
  };
}
