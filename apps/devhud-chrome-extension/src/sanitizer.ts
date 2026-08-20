export const MaximumOuterHtmlBytes = 128 * 1024;

const allowedElements = new Set(["a", "article", "aside", "blockquote", "code", "dd", "details", "div", "dl", "dt", "em", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "img", "li", "main", "nav", "ol", "p", "pre", "section", "summary", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul"]);
export const AllowedAccessibilityAttributes = new Set(["alt", "aria-describedby", "aria-hidden", "aria-label", "aria-labelledby", "role", "title"]);

export function normalizeCapturedUrl(input: string): string {
  const url = new URL(input);
  if (url.protocol !== "http:" && url.protocol !== "https:") throw new TypeError("unsupported URL");
  url.username = "";
  url.password = "";
  url.search = "";
  url.hash = "";
  url.pathname = url.pathname.split("/").map((segment) => segment === "" ? "" : "<redacted>").join("/");
  return url.href;
}

function isHidden(element: Element): boolean {
  if (element.hasAttribute("hidden") || element.getAttribute("aria-hidden")?.toLowerCase() === "true") return true;
  const html = element as HTMLElement;
  if (html.style?.display === "none" || html.style?.visibility === "hidden") return true;
  return false;
}

function appendSanitized(parent: DocumentFragment | Element, node: Node, document: Document): void {
  if (node.nodeType === Node.TEXT_NODE) {
    parent.append(document.createTextNode(node.textContent ?? ""));
    return;
  }
  if (!(node instanceof Element) || !allowedElements.has(node.localName) || isHidden(node)) return;
  const clean = document.createElement(node.localName);
  for (const attribute of Array.from(node.attributes)) {
    const name = attribute.name.toLowerCase();
    if (AllowedAccessibilityAttributes.has(name) && !(name === "aria-hidden" && attribute.value.toLowerCase() === "true")) clean.setAttribute(name, attribute.value);
  }
  for (const child of Array.from(node.childNodes)) appendSanitized(clean, child, document);
  parent.append(clean);
}

export function sanitizeOuterHtml(value: string): string {
  const parsed = new DOMParser().parseFromString(value, "text/html");
  const fragment = parsed.createDocumentFragment();
  for (const node of Array.from(parsed.body.childNodes)) appendSanitized(fragment, node, parsed);
  const container = parsed.createElement("div");
  container.append(fragment);
  while (new TextEncoder().encode(container.innerHTML).byteLength > MaximumOuterHtmlBytes && container.lastChild) container.lastChild.remove();
  return new TextEncoder().encode(container.innerHTML).byteLength <= MaximumOuterHtmlBytes ? container.innerHTML : "";
}

export function sanitizeAccessibility(element: Element | null): Readonly<Record<string, string>> {
  if (!element) return {};
  return Object.fromEntries(Array.from(element.attributes)
    .map((attribute) => [attribute.name.toLowerCase(), attribute.value] as const)
    .filter(([name, value]) => AllowedAccessibilityAttributes.has(name) && !(name === "aria-hidden" && value.toLowerCase() === "true")));
}
