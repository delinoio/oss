// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useState, type ReactNode } from "react";
import { ProductReferenceLabels, ProductReferenceKind, productDiagnosticText } from "@delinoio/delidev-api-client";
import { displayLocale, useLocale } from "./localization";
export { ProductReferenceKind, productDiagnosticText };
const References = createContext<ProductReferenceLabels | undefined>(undefined);
export function ProductReferenceProvider({ children }: { children: ReactNode }) {
  const [labels] = useState(() => new ProductReferenceLabels());
  return <References.Provider value={labels}>{children}</References.Provider>;
}
export function useProductReferenceLabels() {
  const shared = useContext(References);
  const [local] = useState(() => new ProductReferenceLabels());
  return shared ?? local;
}
export function useProductReferences() {
  useLocale();
  const labels = useProductReferenceLabels();
  return (id: string | undefined, kind = ProductReferenceKind.Resource, name?: string) => labels.label(id, kind, displayLocale(), name);
}
export function ProductReference({ value, kind = ProductReferenceKind.Resource, name }: { value?: string; kind?: ProductReferenceKind; name?: string }) {
  const reference = useProductReferences();
  return <>{reference(value, kind, name)}</>;
}
