import { useEffect, useId, useRef, useState } from "react";

import "./styles.css";

export enum DocsProductId {
  PublicDocs = "public-docs",
  Runmoor = "runmoor",
  Nodeup = "nodeup",
  Binpm = "binpm",
}

export interface DocsProduct {
  readonly id: DocsProductId;
  readonly label: string;
  readonly href: string;
}

export const docsProducts: readonly DocsProduct[] = [
  {
    id: DocsProductId.PublicDocs,
    label: "Delino OSS",
    href: "https://oss.delino.io/",
  },
  {
    id: DocsProductId.Runmoor,
    label: "Runmoor",
    href: "https://oss.delino.io/runmoor",
  },
  {
    id: DocsProductId.Nodeup,
    label: "Nodeup",
    href: "https://nodeup.delino.io",
  },
  {
    id: DocsProductId.Binpm,
    label: "binpm",
    href: "https://binpm.delino.io",
  },
];

function publicDocsRouteProduct(currentProduct: DocsProductId) {
  if (
    currentProduct === DocsProductId.PublicDocs
    && typeof window !== "undefined"
    && /^\/runmoor(?:\/|$)/u.test(window.location.pathname)
  ) {
    return DocsProductId.Runmoor;
  }

  return currentProduct;
}

export function DocsProductSwitcher({ currentProduct }: { readonly currentProduct: DocsProductId }) {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const panelId = `docs-product-switcher-${useId().replace(/:/gu, "")}`;
  const selectedProduct = publicDocsRouteProduct(currentProduct);
  const selected = docsProducts.find((product) => product.id === selectedProduct) ?? docsProducts[0];

  useEffect(() => {
    if (!open) return undefined;

    function closeOnPointerDown(event: PointerEvent) {
      const target = event.target;
      if (target instanceof Node && !panelRef.current?.parentElement?.contains(target)) {
        setOpen(false);
      }
    }

    function closeOnEscape(event: KeyboardEvent) {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
      triggerRef.current?.focus();
    }

    document.addEventListener("pointerdown", closeOnPointerDown);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnPointerDown);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  return (
    <div className="delino-docs-product-switcher">
      <button
        ref={triggerRef}
        aria-controls={panelId}
        aria-expanded={open}
        aria-label="Select documentation site"
        className="delino-docs-product-switcher__trigger"
        type="button"
        onClick={() => setOpen((current) => !current)}
      >
        <span>{selected.label}</span>
        <span aria-hidden="true" className="delino-docs-product-switcher__chevron">⌄</span>
      </button>
      <div
        ref={panelRef}
        className="delino-docs-product-switcher__panel"
        hidden={!open}
        id={panelId}
      >
        <nav aria-label="Documentation products">
          <ul className="delino-docs-product-switcher__list">
            {docsProducts.map((product) => {
              const isSelected = product.id === selectedProduct;
              return (
                <li key={product.id}>
                  <a
                    aria-current={isSelected ? "page" : undefined}
                    className="delino-docs-product-switcher__link"
                    href={product.href}
                    onClick={() => setOpen(false)}
                  >
                    <span>{product.label}</span>
                    {isSelected && (
                      <span aria-hidden="true" className="delino-docs-product-switcher__check">✓</span>
                    )}
                  </a>
                </li>
              );
            })}
          </ul>
        </nav>
      </div>
    </div>
  );
}
