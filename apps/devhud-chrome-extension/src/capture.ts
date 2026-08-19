export interface CapturedBrowserContext {
  readonly url: string;
  readonly title: string;
  readonly viewport: { readonly width: number; readonly height: number };
  readonly userAgent: string;
  readonly selectedBounds: { readonly x: number; readonly y: number; readonly width: number; readonly height: number } | null;
  readonly accessibility: Readonly<Record<string, string>>;
  readonly outerHtml: string;
}

interface InjectedCapturedBrowserContext extends CapturedBrowserContext {
  readonly liveUrl: string;
}

/**
 * This function is serialized by chrome.scripting.executeScript, so every
 * helper and constant it uses must remain inside the function body.
 */
export function injectedCapture(selectElement: boolean) {
  const allowedElements = new Set(["a", "article", "aside", "blockquote", "code", "dd", "details", "div", "dl", "dt", "em", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "img", "li", "main", "nav", "ol", "p", "pre", "section", "summary", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul"]);
  const allowedAttributes = new Set(["alt", "aria-describedby", "aria-hidden", "aria-label", "aria-labelledby", "role", "title"]);
  const encoder = new TextEncoder();
  const truncateUtf8 = (value: string, maximumBytes: number) => {
    if (encoder.encode(value).byteLength <= maximumBytes) return value;
    let output = "";
    let bytes = 0;
    for (const character of value) {
      const characterBytes = encoder.encode(character).byteLength;
      if (bytes + characterBytes > maximumBytes) break;
      output += character;
      bytes += characterBytes;
    }
    return output;
  };
  const normalizeUrl = () => {
    const url = new URL(location.href);
    if (!/^https?:$/u.test(url.protocol)) throw new TypeError("unsupported URL");
    const origin = `${url.protocol}//${url.hostname.toLowerCase()}${url.port ? `:${url.port}` : ""}`;
    const path = url.pathname.split("/").map((segment) => segment === "" ? "" : "<redacted>").join("/");
    const normalized = `${origin}${path}`;
    return encoder.encode(normalized).byteLength <= 16 * 1024 ? normalized : `${origin}${url.pathname === "/" ? "/" : "/<redacted>"}`;
  };
  const isAllowedAndVisible = (element: Element) => {
    if (!allowedElements.has(element.localName) || element.hasAttribute("hidden") || element.getAttribute("aria-hidden")?.toLowerCase() === "true") return false;
    const computed = getComputedStyle(element);
    return computed.display !== "none" && computed.visibility !== "hidden";
  };
  const sanitize = (selected: Element | null) => {
    if (!selected) return "";
    const append = (parent: DocumentFragment | Element, node: Node): void => {
      if (node.nodeType === Node.TEXT_NODE) { parent.append(document.createTextNode(node.textContent ?? "")); return; }
      if (!(node instanceof Element) || !isAllowedAndVisible(node)) return;
      const clean = document.createElement(node.localName);
      for (const attribute of Array.from(node.attributes)) {
        const name = attribute.name.toLowerCase();
        if (allowedAttributes.has(name) && !(name === "aria-hidden" && attribute.value.toLowerCase() === "true")) clean.setAttribute(name, truncateUtf8(attribute.value, 4 * 1024));
      }
      for (const child of Array.from(node.childNodes)) append(clean, child);
      parent.append(clean);
    };
    const fragment = document.createDocumentFragment();
    append(fragment, selected);
    const container = document.createElement("div");
    container.append(fragment);
    while (encoder.encode(container.innerHTML).byteLength > 128 * 1024 && container.lastChild) container.lastChild.remove();
    return container.innerHTML;
  };
  const result = (selected: Element | null): InjectedCapturedBrowserContext => {
    const allowedSelection = selected && isAllowedAndVisible(selected) ? selected : null;
    const bounds = allowedSelection?.getBoundingClientRect();
    const attributes = allowedSelection ? Array.from(allowedSelection.attributes).map((attribute) => [attribute.name.toLowerCase(), attribute.value] as [string, string]) : [];
    const accessibility = Object.fromEntries(attributes
      .filter(([name, value]) => allowedAttributes.has(name) && !(name === "aria-hidden" && value.toLowerCase() === "true"))
      .map(([name, value]) => [name, truncateUtf8(value, 4 * 1024)]));
    return {
      liveUrl: location.href,
      url: normalizeUrl(),
      title: truncateUtf8(document.title, 4 * 1024),
      viewport: { width: innerWidth, height: innerHeight },
      userAgent: truncateUtf8(navigator.userAgent, 4 * 1024),
      selectedBounds: bounds ? { x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height } : null,
      accessibility,
      outerHtml: sanitize(allowedSelection),
    };
  };
  if (!selectElement) return Promise.resolve(result(null));
  return new Promise<InjectedCapturedBrowserContext | null>((resolve) => {
    let timeout: ReturnType<typeof setTimeout> | undefined;
    const cleanup = () => { if (timeout) clearTimeout(timeout); document.removeEventListener("click", click, true); document.removeEventListener("keydown", key, true); };
    const click = (event: MouseEvent) => { event.preventDefault(); event.stopPropagation(); cleanup(); resolve(result(event.target instanceof Element ? event.target : null)); };
    const key = (event: KeyboardEvent) => { if (event.key === "Escape") { event.preventDefault(); cleanup(); resolve(null); } };
    timeout = setTimeout(() => { cleanup(); resolve(null); }, 30_000);
    document.addEventListener("click", click, true);
    document.addEventListener("keydown", key, true);
  });
}
