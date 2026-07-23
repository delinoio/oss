import { useEffect } from "react";

import { canonicalOrigin } from "../config";

export function useDocumentMetadata(title: string, description: string): void {
  useEffect(() => {
    document.title = `${title} — DeliDev`;
    const descriptionElement = document.querySelector<HTMLMetaElement>(
      'meta[name="description"]',
    );
    descriptionElement?.setAttribute("content", description);

    const canonical = document.querySelector<HTMLLinkElement>(
      'link[rel="canonical"]',
    );
    canonical?.setAttribute(
      "href",
      new URL(window.location.pathname, canonicalOrigin).href,
    );
  }, [description, title]);
}
