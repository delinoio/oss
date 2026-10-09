// SPDX-License-Identifier: Apache-2.0
/** Keep the exact active option visible without scrolling the centered dialog
 * or any enclosing source. The reserved results area owns this scroll. */
export function revealWorkerModelOption(option: HTMLElement | null | undefined) {
  const region = option?.closest<HTMLElement>(".worker-model-results");
  if (!option || !region || option.closest("[hidden], [inert], [aria-hidden='true']")) return;
  for (let ancestor: HTMLElement | null = option; ancestor; ancestor = ancestor.parentElement) {
    if (ancestor instanceof HTMLDetailsElement && !ancestor.open) return;
  }
  const bounds = region.getBoundingClientRect(), row = option.getBoundingClientRect();
  if (row.bottom - row.top > bounds.bottom - bounds.top) region.scrollTop += row.top - bounds.top;
  else if (row.top < bounds.top) region.scrollTop += row.top - bounds.top;
  else if (row.bottom > bounds.bottom) region.scrollTop += row.bottom - bounds.bottom;
}
