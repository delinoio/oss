export type BrowserContextState =
  | { readonly kind: "absent" }
  | { readonly kind: "denied" }
  | { readonly kind: "revoked" }
  | { readonly kind: "disconnected" }
  | { readonly kind: "incognito" }
  | { readonly kind: "malformed" }
  | { readonly kind: "sanitized"; readonly context: SanitizedBrowserContext };

export interface SanitizedBrowserContext {
  readonly url: string;
  readonly title: string;
  readonly viewport: { readonly width: number; readonly height: number };
  readonly userAgent: string;
  readonly selectedBounds: { readonly x: number; readonly y: number; readonly width: number; readonly height: number } | null;
  readonly accessibility: Readonly<Record<string, string>>;
  readonly outerHtml: string;
}

export type BrowserContextSource = "chrome" | "contract-permitted-other";

const maxSanitizedOuterHtmlBytes = 128 * 1024;
const allowedElements = new Set(["a", "article", "aside", "blockquote", "code", "dd", "details", "div", "dl", "dt", "em", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "img", "li", "main", "nav", "ol", "p", "pre", "section", "summary", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul"]);
const allowedAttributes = new Set(["alt", "aria-describedby", "aria-hidden", "aria-label", "aria-labelledby", "role", "title"]);

/** Chrome can never include query or fragment data; other sources need consent. */
export function queryFragmentWarningRequired(source: BrowserContextSource, includeQueryOrFragment: boolean): boolean {
  if (source === "chrome" && includeQueryOrFragment) throw new TypeError("Chrome context cannot include query or fragment data");
  return source === "contract-permitted-other" && includeQueryOrFragment;
}

export function sanitizeChromeContext(input: unknown): BrowserContextState {
  try {
    if (!isChromeContextInput(input)) return { kind: "malformed" };
    const url = new URL(input.url);
    if (url.protocol !== "http:" && url.protocol !== "https:") throw new Error();
    const path = url.pathname.split("/").map((segment) => segment === "" ? "" : "<redacted>").join("/");
    return {
      kind: "sanitized",
      context: {
        url: `${url.protocol}//${url.hostname.toLowerCase()}${url.port ? `:${url.port}` : ""}${path}`,
        title: input.title,
        viewport: { width: input.viewport.width, height: input.viewport.height },
        userAgent: input.userAgent,
        selectedBounds: input.selectedBounds === null ? null : {
          x: input.selectedBounds.x,
          y: input.selectedBounds.y,
          width: input.selectedBounds.width,
          height: input.selectedBounds.height,
        },
        accessibility: sanitizeAccessibility(input.accessibility),
        outerHtml: sanitizeOuterHtml(input.outerHtml),
      },
    };
  } catch { return { kind: "malformed" }; }
}

function isChromeContextInput(value: unknown): value is Omit<SanitizedBrowserContext, "url"> & { readonly url: string } {
  if (!isRecord(value) || typeof value.url !== "string" || typeof value.title !== "string" || typeof value.userAgent !== "string" || typeof value.outerHtml !== "string") return false;
  return isViewport(value.viewport) && (value.selectedBounds === null || isSelectedBounds(value.selectedBounds)) && isStringRecord(value.accessibility);
}

function isViewport(value: unknown): value is SanitizedBrowserContext["viewport"] {
  return isRecord(value) && isFiniteNumber(value.width) && isFiniteNumber(value.height);
}

function isSelectedBounds(value: unknown): value is NonNullable<SanitizedBrowserContext["selectedBounds"]> {
  return isRecord(value) && isFiniteNumber(value.x) && isFiniteNumber(value.y) && isFiniteNumber(value.width) && isFiniteNumber(value.height);
}

function isStringRecord(value: unknown): value is Readonly<Record<string, string>> {
  return isRecord(value) && Object.values(value).every((entry) => typeof entry === "string");
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value);
}

function sanitizeAccessibility(value: Readonly<Record<string, string>>): Readonly<Record<string, string>> {
  return Object.fromEntries(Object.entries(value).filter(([name]) => allowedAttributes.has(name.toLowerCase())));
}

function sanitizeOuterHtml(value: string): string {
  const parsed = new DOMParser().parseFromString(value, "text/html");
  const fragment = document.createDocumentFragment();
  for (const node of Array.from(parsed.body.childNodes)) appendSanitizedNode(fragment, node);
  const container = document.createElement("div");
  container.append(fragment);
  const sanitized = container.innerHTML;
  return new TextEncoder().encode(sanitized).byteLength <= maxSanitizedOuterHtmlBytes ? sanitized : "";
}

function appendSanitizedNode(parent: DocumentFragment | HTMLElement, node: Node): void {
  if (node.nodeType === Node.TEXT_NODE) {
    parent.append(document.createTextNode(node.textContent ?? ""));
    return;
  }
  if (!(node instanceof Element) || !allowedElements.has(node.localName)) return;
  const sanitized = document.createElement(node.localName);
  for (const attribute of Array.from(node.attributes)) if (allowedAttributes.has(attribute.name.toLowerCase())) sanitized.setAttribute(attribute.name.toLowerCase(), attribute.value);
  for (const child of Array.from(node.childNodes)) appendSanitizedNode(sanitized, child);
  parent.append(sanitized);
}
