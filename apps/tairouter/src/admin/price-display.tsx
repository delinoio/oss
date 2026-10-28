// SPDX-License-Identifier: Apache-2.0
import { useId } from "react";
import type { PriceOverride } from "@tairouter/client";
import { t as translate, useLocale } from "../i18n";

// Display label for a stored service-tier selector. The result is for display
// only and must never be submitted as an RPC value.
export function tierLabel(serviceTier: string) {
  return serviceTier === ""
    ? translate("allTiersFallback")
    : translate("tierValue", { value0: serviceTier });
}

// Accessible record name used to make per-record actions unambiguous.
export function priceRecordName(
  price: Pick<PriceOverride, "providerId" | "modelId" | "serviceTier">,
) {
  return translate("priceRecordName", {
    value0: price.providerId,
    value1: price.modelId,
    value2: tierLabel(price.serviceTier),
  });
}

export enum PriceRateField {
  Input = "input",
  Output = "output",
  CacheRead = "cacheRead",
  CacheWrite = "cacheWrite",
  CacheWriteShort = "cacheWriteShort",
  CacheWriteLong = "cacheWriteLong",
}

// Rate fields in the order they appear in the list, review and editor.
export const priceRateFields = () =>
  [
    [PriceRateField.Input, translate("input")],
    [PriceRateField.Output, translate("output")],
    [PriceRateField.CacheRead, translate("cacheRead")],
    [PriceRateField.CacheWrite, translate("cacheWrite")],
    [PriceRateField.CacheWriteShort, translate("shortCacheWrite")],
    [PriceRateField.CacheWriteLong, translate("longCacheWrite")],
  ] as const;

// Read-only definition list of all six token rates. A missing rate reads
// Unknown, an explicit zero stays 0, and decimal strings are rendered verbatim.
export function PriceRates({ rates }: { rates?: PriceOverride["rates"] }) {
  useLocale();
  const unitId = useId();
  return (
    <>
      <p id={unitId} className="admin-price-unit">
        {translate("currencyPerMillionTokens", {
          value0: rates?.currency || translate("unknown"),
        })}
      </p>
      <dl className="admin-price-rates">
        {priceRateFields().map(([field, name]) => (
          <div key={field}>
            <dt>{name}</dt>
            <dd aria-describedby={unitId}>
              {rates?.[field] ?? translate("unknown")}
            </dd>
          </div>
        ))}
      </dl>
    </>
  );
}
