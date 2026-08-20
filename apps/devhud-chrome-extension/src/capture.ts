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
export function injectedCapture(selectElement: boolean, language: "en" | "ko" = "en") {
  const allowedElements = new Set(["a", "article", "aside", "blockquote", "code", "dd", "details", "div", "dl", "dt", "em", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "img", "li", "main", "nav", "ol", "p", "pre", "section", "summary", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul"]);
  const allowedAttributes = new Set(["alt", "aria-describedby", "aria-hidden", "aria-label", "aria-labelledby", "role", "title"]);
  const clippingOverflow = new Set(["auto", "clip", "hidden", "scroll"]);
  const voidElements = new Set(["hr", "img"]);
  const encoder = new TextEncoder();
  const truncateUtf8 = (value: string, maximumBytes: number) => {
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
    const cssVisible = typeof element.checkVisibility === "function"
      ? element.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true, contentVisibilityAuto: true })
      : ancestors.every((current) => {
          const computed = getComputedStyle(current);
          const contentVisibility = computed.getPropertyValue("content-visibility");
          return computed.display !== "none"
            && computed.visibility !== "hidden"
            && computed.visibility !== "collapse"
            && Number.parseFloat(computed.opacity) !== 0
            && (contentVisibility === "" || contentVisibility === "visible");
        });
    if (!cssVisible || !Number.isFinite(innerWidth) || !Number.isFinite(innerHeight) || innerWidth <= 0 || innerHeight <= 0) return false;
    const bounds = element.getBoundingClientRect();
    if (![bounds.x, bounds.y, bounds.width, bounds.height].every(Number.isFinite) || bounds.width <= 0 || bounds.height <= 0) return false;
    let left = Math.max(0, bounds.x);
    let top = Math.max(0, bounds.y);
    let right = Math.min(innerWidth, bounds.x + bounds.width);
    let bottom = Math.min(innerHeight, bounds.y + bounds.height);
    if (right <= left || bottom <= top) return false;
    for (const ancestor of ancestors.slice(1)) {
      const computed = getComputedStyle(ancestor);
      const overflow = computed.overflow.trim().split(/\s+/u).filter(Boolean);
      const overflowX = computed.overflowX || overflow[0] || "visible";
      const overflowY = computed.overflowY || overflow[1] || overflow[0] || "visible";
      const clipX = clippingOverflow.has(overflowX);
      const clipY = clippingOverflow.has(overflowY);
      if (!clipX && !clipY) continue;
      const ancestorBounds = ancestor.getBoundingClientRect();
      if (clipX) {
        if (![ancestorBounds.x, ancestorBounds.width].every(Number.isFinite) || ancestorBounds.width <= 0) return false;
        left = Math.max(left, ancestorBounds.x);
        right = Math.min(right, ancestorBounds.x + ancestorBounds.width);
      }
      if (clipY) {
        if (![ancestorBounds.y, ancestorBounds.height].every(Number.isFinite) || ancestorBounds.height <= 0) return false;
        top = Math.max(top, ancestorBounds.y);
        bottom = Math.min(bottom, ancestorBounds.y + ancestorBounds.height);
      }
      if (right <= left || bottom <= top) return false;
    }
    return true;
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
    const selectionText = language === "ko"
      ? {
          title: "DevHUD 요소 선택",
          instructions: "Tab 또는 Shift+Tab으로 요소를 이동하고 Enter 또는 스페이스바로 선택하세요. Esc를 누르면 취소됩니다.",
          current: (position: number, count: number, name: string) => `${count}개 중 ${position}번째: ${name}`,
          empty: "선택할 수 있는 요소가 없습니다. Esc를 눌러 취소하세요.",
        }
      : {
          title: "DevHUD element selection",
          instructions: "Press Tab or Shift+Tab to move between elements, Enter or Space to select, or Escape to cancel.",
          current: (position: number, count: number, name: string) => `${position} of ${count}: ${name}`,
          empty: "No selectable elements are available. Press Escape to cancel.",
        };
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const overlay = document.createElement("iframe");
    overlay.title = selectionText.title;
    overlay.tabIndex = 0;
    for (const [property, value] of Object.entries({
      position: "fixed",
      inset: "0",
      width: "100vw",
      height: "100vh",
      border: "0",
      margin: "0",
      padding: "0",
      background: "transparent",
      zIndex: "2147483647",
    })) overlay.style.setProperty(property.replace(/[A-Z]/gu, (character) => `-${character.toLowerCase()}`), value, "important");
    document.documentElement.append(overlay);
    const overlayWindow = overlay.contentWindow;
    const overlayDocument = overlay.contentDocument;
    if (!overlayWindow || !overlayDocument) {
      overlay.remove();
      resolve(null);
      return;
    }
    overlayDocument.documentElement.style.setProperty("cursor", "crosshair", "important");
    overlayDocument.documentElement.style.setProperty("min-height", "100%", "important");
    overlayDocument.documentElement.lang = language;
    overlayDocument.body.tabIndex = -1;
    overlayDocument.body.setAttribute("role", "dialog");
    overlayDocument.body.setAttribute("aria-modal", "true");
    overlayDocument.body.setAttribute("aria-label", selectionText.title);
    overlayDocument.body.style.setProperty("min-height", "100vh", "important");
    overlayDocument.body.style.setProperty("margin", "0", "important");
    overlayDocument.body.style.setProperty("background", "transparent", "important");
    const panel = overlayDocument.createElement("div");
    for (const [property, value] of Object.entries({
      position: "fixed",
      inset: "16px auto auto 16px",
      maxWidth: "min(520px, calc(100vw - 32px))",
      padding: "12px 16px",
      borderRadius: "8px",
      color: "white",
      background: "rgba(17, 24, 39, 0.94)",
      font: "14px/1.5 system-ui, sans-serif",
      pointerEvents: "none",
      zIndex: "2",
    })) panel.style.setProperty(property.replace(/[A-Z]/gu, (character) => `-${character.toLowerCase()}`), value, "important");
    const instructions = overlayDocument.createElement("p");
    instructions.textContent = selectionText.instructions;
    instructions.style.setProperty("margin", "0", "important");
    const status = overlayDocument.createElement("p");
    status.setAttribute("aria-live", "polite");
    status.style.setProperty("margin", "8px 0 0", "important");
    const highlight = overlayDocument.createElement("div");
    for (const [property, value] of Object.entries({
      position: "fixed",
      display: "none",
      boxSizing: "border-box",
      border: "3px solid #2563eb",
      background: "rgba(37, 99, 235, 0.16)",
      pointerEvents: "none",
      zIndex: "1",
    })) highlight.style.setProperty(property.replace(/[A-Z]/gu, (character) => `-${character.toLowerCase()}`), value, "important");
    panel.append(instructions, status);
    overlayDocument.body.append(highlight, panel);
    const selector = Array.from(allowedElements).join(",");
    // Keep candidate discovery linear. Visibility is checked only for the active
    // candidate so deeply nested pages do not turn picker startup into an O(n²)
    // ancestor walk.
    const candidates = Array.from(document.querySelectorAll(selector));
    let initialCandidate: Element | null = previousFocus;
    while (initialCandidate && !isAllowedAndVisible(initialCandidate)) initialCandidate = initialCandidate.parentElement;
    let candidateIndex = initialCandidate ? candidates.indexOf(initialCandidate) : -1;
    if (candidateIndex < 0 && candidates.length > 0) candidateIndex = 0;
    const currentCandidate = () => {
      const candidate = candidates[candidateIndex];
      return candidate?.isConnected && isAllowedAndVisible(candidate) ? candidate : null;
    };
    const describeCandidate = (candidate: Element) => {
      const label = ["aria-label", "title", "alt"]
        .map((name) => candidate.getAttribute(name)?.trim())
        .find((value) => value);
      return label ? `${candidate.localName}: ${truncateUtf8(label, 256)}` : candidate.localName;
    };
    const updateCandidate = () => {
      const candidate = currentCandidate();
      if (!candidate) {
        highlight.style.setProperty("display", "none", "important");
        status.textContent = selectionText.empty;
        return;
      }
      const bounds = candidate.getBoundingClientRect();
      const hasBounds = [bounds.x, bounds.y, bounds.width, bounds.height].every(Number.isFinite)
        && bounds.width > 0
        && bounds.height > 0;
      if (hasBounds) {
        for (const [property, value] of Object.entries({
          display: "block",
          left: `${bounds.x}px`,
          top: `${bounds.y}px`,
          width: `${bounds.width}px`,
          height: `${bounds.height}px`,
        })) highlight.style.setProperty(property, value, "important");
      } else {
        highlight.style.setProperty("display", "none", "important");
      }
      status.textContent = selectionText.current(candidateIndex + 1, candidates.length, describeCandidate(candidate));
    };
    const moveCandidate = (direction: 1 | -1) => {
      if (candidates.length === 0) return;
      for (let attempt = 0; attempt < candidates.length; attempt += 1) {
        candidateIndex = (candidateIndex + direction + candidates.length) % candidates.length;
        if (currentCandidate()) break;
      }
      updateCandidate();
    };
    let timeout: ReturnType<typeof setTimeout> | undefined;
    const pointerEvents = ["pointerdown", "pointerup", "mousedown", "mouseup", "touchstart", "touchend", "contextmenu"];
    const suppress = (event: Event) => { event.preventDefault(); event.stopImmediatePropagation(); };
    const cleanup = () => {
      if (timeout) clearTimeout(timeout);
      for (const eventName of pointerEvents) overlayWindow.removeEventListener(eventName, suppress, true);
      overlayWindow.removeEventListener("click", click, true);
      overlayWindow.removeEventListener("keydown", key, true);
      document.removeEventListener("keydown", key, true);
      overlay.remove();
      if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true });
    };
    const click = (event: MouseEvent) => {
      suppress(event);
      overlay.style.setProperty("pointer-events", "none", "important");
      const selected = document.elementFromPoint(event.clientX, event.clientY);
      const captured = result(selected);
      cleanup();
      resolve(captured);
    };
    const key = (event: KeyboardEvent) => {
      if (event.key === "Escape") { suppress(event); cleanup(); resolve(null); return; }
      if (event.key === "Tab") { suppress(event); moveCandidate(event.shiftKey ? -1 : 1); return; }
      if (event.key === "Enter" || event.key === " ") {
        suppress(event);
        const candidate = currentCandidate();
        if (!candidate) return;
        const captured = result(candidate);
        cleanup();
        resolve(captured);
      }
    };
    timeout = setTimeout(() => { cleanup(); resolve(null); }, 30_000);
    for (const eventName of pointerEvents) overlayWindow.addEventListener(eventName, suppress, { capture: true, passive: false });
    overlayWindow.addEventListener("click", click, true);
    overlayWindow.addEventListener("keydown", key, true);
    document.addEventListener("keydown", key, true);
    updateCandidate();
    overlayDocument.body.focus({ preventScroll: true });
  });
}
