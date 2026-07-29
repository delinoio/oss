/**
 * Runs in the selected page through chrome.scripting.executeScript. Keep this
 * function self-contained: Chrome serializes it rather than its module scope.
 */
export function selectDomBoundary() {
  const stableToken = (value) =>
    typeof value === "string" &&
    /^[A-Za-z_][A-Za-z0-9_-]{0,63}$/u.test(value) &&
    !/[0-9]{4,}/u.test(value) &&
    !/^[a-f0-9]{16,}$/iu.test(value);
  const segment = (element) => {
    const tag = element.localName;
    if (stableToken(element.id)) return `${tag}#${element.id.toLowerCase()}`;
    const className = [...element.classList].find(stableToken);
    if (className !== undefined) return `${tag}.${className.toLowerCase()}`;
    const siblings = element.parentElement === null
      ? []
      : [...element.parentElement.children].filter(
          (candidate) => candidate.localName === tag,
        );
    return siblings.length > 1
      ? `${tag}:nth-of-type(${siblings.indexOf(element) + 1})`
      : tag;
  };
  const selectorFor = (element) => {
    const segments = [];
    let current = element;
    while (current !== null && segments.length < 8) {
      segments.unshift(segment(current));
      if (stableToken(current.id)) break;
      current = current.parentElement;
    }
    return segments.join(" > ").slice(0, 512);
  };
  const boundaryFor = (element) => {
    const rect = element.getBoundingClientRect();
    const x = Math.max(0, Math.min(innerWidth, rect.left));
    const y = Math.max(0, Math.min(innerHeight, rect.top));
    const right = Math.max(x, Math.min(innerWidth, rect.right));
    const bottom = Math.max(y, Math.min(innerHeight, rect.bottom));
    return { x, y, width: right - x, height: bottom - y };
  };
  const text = (value, maximum) => {
    if (typeof value !== "string") return undefined;
    const normalized = [...value]
      .map((character) => {
        const code = character.codePointAt(0);
        return code !== undefined && (code < 32 || code === 127) ? " " : character;
      })
      .join("")
      .replace(/\s+/gu, " ")
      .trim();
    return normalized.length === 0 ? undefined : normalized.slice(0, maximum);
  };
  const accessibleNameFor = (element) => {
    const ariaLabel = text(element.getAttribute("aria-label"), 256);
    if (ariaLabel !== undefined) return ariaLabel;
    const labelledBy = element.getAttribute("aria-labelledby");
    if (labelledBy === null) return undefined;
    return text(
      labelledBy
        .split(/\s+/u)
        .slice(0, 8)
        .map((id) => document.getElementById(id)?.textContent ?? "")
        .join(" "),
      256,
    );
  };

  return new Promise((resolve) => {
    const overlay = document.createElement("div");
    overlay.setAttribute("aria-hidden", "true");
    Object.assign(overlay.style, {
      position: "fixed",
      zIndex: "2147483647",
      pointerEvents: "none",
      border: "2px solid #6d5dfc",
      background: "rgba(109, 93, 252, 0.12)",
      display: "none",
    });
    document.documentElement.append(overlay);
    let target;
    const move = (event) => {
      target = event.target instanceof Element ? event.target : undefined;
      if (target === undefined || target === overlay) return;
      const boundary = boundaryFor(target);
      Object.assign(overlay.style, {
        display: "block",
        left: `${boundary.x}px`,
        top: `${boundary.y}px`,
        width: `${boundary.width}px`,
        height: `${boundary.height}px`,
      });
    };
    const finish = (result) => {
      document.removeEventListener("pointermove", move, true);
      document.removeEventListener("click", click, true);
      document.removeEventListener("keydown", key, true);
      overlay.remove();
      resolve(result);
    };
    const click = (event) => {
      event.preventDefault();
      event.stopImmediatePropagation();
      const clicked =
        event.target instanceof Element && event.target !== overlay
          ? event.target
          : target;
      if (clicked === undefined) return finish(null);
      const role = text(clicked.getAttribute("role"), 64);
      const accessibleName = accessibleNameFor(clicked);
      finish({
        boundary: boundaryFor(clicked),
        selector: selectorFor(clicked),
        tag: clicked.localName.slice(0, 32),
        ...(role === undefined ? {} : { role }),
        ...(accessibleName === undefined ? {} : { accessibleName }),
        viewport: {
          width: innerWidth,
          height: innerHeight,
          devicePixelRatio,
        },
      });
    };
    const key = (event) => {
      if (event.key === "Escape") {
        event.preventDefault();
        finish(null);
      }
    };
    document.addEventListener("pointermove", move, true);
    document.addEventListener("click", click, true);
    document.addEventListener("keydown", key, true);
  });
}
