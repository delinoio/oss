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
  const voidElements = new Set(["hr", "img"]);
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
    return `${origin}${path}`;
  };
  const isAllowedAndVisible = (element: Element) => {
    if (!allowedElements.has(element.localName)) return false;
    const ancestors: Element[] = [];
    for (let current: Element | null = element; current; current = current.parentElement) {
      if (current.hasAttribute("hidden") || current.getAttribute("aria-hidden")?.toLowerCase() === "true") return false;
      ancestors.push(current);
    }
    if (typeof element.checkVisibility === "function") {
      return element.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true, contentVisibilityAuto: true });
    }
    return ancestors.every((current) => {
      const computed = getComputedStyle(current);
      const contentVisibility = computed.getPropertyValue("content-visibility");
      return computed.display !== "none"
        && computed.visibility !== "hidden"
        && computed.visibility !== "collapse"
        && Number.parseFloat(computed.opacity) !== 0
        && (contentVisibility === "" || contentVisibility === "visible");
    });
  };
  const sanitize = (selected: Element | null) => {
    if (!selected) return "";
    const maximumBytes = 128 * 1024;
    const fragments: string[] = [];
    const textProbe = document.createElement("div");
    let encodedBytes = 0;
    let exhausted = false;
    const appendSerializedText = (value: string) => {
      let chunk = "";
      const appendChunk = (candidate: string) => {
        textProbe.textContent = candidate;
        const serialized = textProbe.innerHTML;
        const bytes = encoder.encode(serialized).byteLength;
        if (encodedBytes + bytes > maximumBytes) return false;
        fragments.push(serialized);
        encodedBytes += bytes;
        return true;
      };
      for (const character of value) {
        chunk += character;
        if (chunk.length < 1024) continue;
        if (!appendChunk(chunk)) {
          for (const remainingCharacter of chunk) {
            if (!appendChunk(remainingCharacter)) return false;
          }
        }
        chunk = "";
      }
      if (chunk && !appendChunk(chunk)) {
        for (const remainingCharacter of chunk) {
          if (!appendChunk(remainingCharacter)) return false;
        }
      }
      return true;
    };
    const stack: Array<{ next: Node | null; includeSiblings: boolean; closingTag: string }> = [
      { next: selected, includeSiblings: false, closingTag: "" },
    ];
    while (stack.length > 0 && !exhausted) {
      const frame = stack[stack.length - 1]!;
      const node = frame.next;
      if (!node) {
        if (frame.closingTag) fragments.push(frame.closingTag);
        stack.pop();
        continue;
      }
      frame.next = frame.includeSiblings ? node.nextSibling : null;
      if (node.nodeType === Node.TEXT_NODE) {
        exhausted = !appendSerializedText(node.textContent ?? "");
        continue;
      }
      if (!(node instanceof Element) || !isAllowedAndVisible(node)) continue;
      const clean = document.createElement(node.localName);
      for (const attribute of Array.from(node.attributes)) {
        const name = attribute.name.toLowerCase();
        if (allowedAttributes.has(name) && !(name === "aria-hidden" && attribute.value.toLowerCase() === "true")) clean.setAttribute(name, truncateUtf8(attribute.value, 4 * 1024));
      }
      const closingTag = voidElements.has(node.localName) ? "" : `</${node.localName}>`;
      const serialized = clean.outerHTML;
      const openingTag = closingTag ? serialized.slice(0, -closingTag.length) : serialized;
      const elementBytes = encoder.encode(openingTag).byteLength + encoder.encode(closingTag).byteLength;
      if (encodedBytes + elementBytes > maximumBytes) {
        exhausted = true;
        continue;
      }
      fragments.push(openingTag);
      encodedBytes += elementBytes;
      if (closingTag) stack.push({ next: node.firstChild, includeSiblings: true, closingTag });
    }
    if (exhausted) {
      for (let index = stack.length - 1; index >= 0; index -= 1) {
        const closingTag = stack[index]!.closingTag;
        if (closingTag) fragments.push(closingTag);
      }
    }
    return fragments.join("");
  };
  const result = (selected: Element | null): InjectedCapturedBrowserContext => {
    const allowedSelection = selected && isAllowedAndVisible(selected) ? selected : null;
    const bounds = allowedSelection?.getBoundingClientRect();
    const selectedBounds = bounds
      && Number.isFinite(bounds.x)
      && Number.isFinite(bounds.y)
      && Number.isFinite(bounds.width)
      && Number.isFinite(bounds.height)
      && bounds.width > 0
      && bounds.height > 0
      ? { x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height }
      : null;
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
      selectedBounds,
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
