// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useMemo, type ReactNode } from "react";
import { copy, useLocale } from "./localization";
import { ProductIdentityKind, ProductIdentityNumbers, productDiagnosticPresentation } from "./product-identity";

// Standalone product views share their original mounted view's fallback scope.
// Connected shells replace it with their exact connection-owned registry.
const IdentityScope = createContext(new ProductIdentityNumbers());
export function ProductIdentityScope({ scope, children }: { scope: string; children: ReactNode }) {
  const numbers = useMemo(() => new ProductIdentityNumbers(), [scope]);
  return <IdentityScope.Provider value={numbers}>{children}</IdentityScope.Provider>;
}
export function useProductIdentity() {
  useLocale();
  const numbers = useContext(IdentityScope);
  const label = (id: string, kind = ProductIdentityKind.Resource, name = "", numbered = false) => {
    const noun = copy(`product-identities.${kind}`);
    if (!id) return name || copy("product-identities.unavailable", { kind: noun });
    const identity = copy("product-identities.numbered", { kind: noun, number: numbers.number(kind, id) });
    return name ? numbered ? `${name} · ${identity}` : name : identity;
  };
  return { label, diagnostic: (value: string) => productDiagnosticPresentation(value, id => label(id, ProductIdentityKind.Reference)) };
}
export function ProductIdentity({ id, kind = ProductIdentityKind.Resource, name = "", numbered = false }: { id: string; kind?: ProductIdentityKind; name?: string; numbered?: boolean }) {
  const identity = useProductIdentity();
  return name ? numbered ? <><span>{name}</span> · {identity.label(id, kind)}</> : <>{name}</> : <>{identity.label(id, kind)}</>;
}
