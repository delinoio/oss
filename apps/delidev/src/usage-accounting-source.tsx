// SPDX-License-Identifier: Apache-2.0
import { useState, type ReactNode } from "react";
import { AccountingUnitKind } from "@delinoio/delidev-api-client";
import { copy, displayLocale, useLocale } from "./localization";

export interface AccountingDisclosures {
  expanded: Partial<Record<AccountingUnitKind, boolean>>;
  change: (kind: AccountingUnitKind, expanded: boolean) => void;
}

/** Keep a source's original units separate, including an explicitly unknown empty interval. */
export function AccountingSource({ kind, title, count, children, disclosures }: { kind: AccountingUnitKind; title: string; count: number; children: ReactNode; disclosures?: AccountingDisclosures }) {
  useLocale();
  const [choice, setChoice] = useState<boolean>();
  const expanded = (disclosures ? disclosures.expanded[kind] : choice) ?? count > 0;
  return <section className="usage-accounting-source" aria-label={title}>
    <details open={expanded} onToggle={(event) => { const next = event.currentTarget.open; if (disclosures) disclosures.change(kind, next); else setChoice(next); }}>
      <summary><span className="usage-source-heading">{title}</span><span>{copy("usage.sourceRecords", { count: count.toLocaleString(displayLocale()) })}</span>{!count ? <span>{copy("usage.extra.ca1844969742")}</span> : null}</summary>
      {!count ? <p className="usage-source-empty-warning">{copy("usage.emptySourceWarning")}</p> : null}
      <div className="usage-source-details">{children}</div>
    </details>
    {!expanded && !count ? <p className="usage-source-empty-warning">{copy("usage.emptySourceWarning")}</p> : null}
  </section>;
}
