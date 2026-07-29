export const NATIVE_HOST_NAME = "dev.deli.devhud.realqa";
export const PROTOCOL_VERSION = 1;
export const MAX_ENCODED_IMAGE_BYTES = 25 * 1024 * 1024;
export const MAX_EXTENSION_MESSAGE_BYTES = 64 * 1024 * 1024;
export const MAX_HOST_RESPONSE_BYTES = 1024 * 1024;

const SAFE_SELECTOR =
  /^(?:[a-z][a-z0-9-]*(?:(?:#[a-z_][a-z0-9_-]*)|(?:\.[a-z_][a-z0-9_-]*)|(?::nth-of-type\([1-9][0-9]{0,4}\)))?)(?: > [a-z][a-z0-9-]*(?:(?:#[a-z_][a-z0-9_-]*)|(?:\.[a-z_][a-z0-9_-]*)|(?::nth-of-type\([1-9][0-9]{0,4}\)))?){0,7}$/u;
const IDENTIFIER = /^[A-Za-z][A-Za-z0-9_-]{0,63}$/u;

function boundedText(value, maximumLength) {
  if (typeof value !== "string") return undefined;
  const normalized = [...value]
    .map((character) => {
      const code = character.codePointAt(0);
      return code !== undefined && (code < 32 || code === 127) ? " " : character;
    })
    .join("")
    .replace(/\s+/gu, " ")
    .trim();
  return normalized.length === 0
    ? undefined
    : normalized.slice(0, maximumLength);
}

export function sanitizePageUrl(value) {
  const text = boundedText(value, 4096);
  if (text === undefined) return undefined;
  try {
    const url = new URL(text);
    url.username = "";
    url.password = "";
    url.search = "";
    url.hash = "";
    return url.toString().slice(0, 2048);
  } catch {
    return undefined;
  }
}

export function sanitizePageTitle(value) {
  return boundedText(value, 256);
}

export function originPatternForUrl(value) {
  try {
    const url = new URL(value);
    if (url.protocol !== "http:" && url.protocol !== "https:") return null;
    return `${url.origin}/*`;
  } catch {
    return null;
  }
}

export function isRestrictedPage(value) {
  return originPatternForUrl(value) === null;
}

export function sanitizeSelector(value) {
  const text = boundedText(value, 512);
  return text !== undefined && SAFE_SELECTOR.test(text.toLowerCase())
    ? text.toLowerCase()
    : undefined;
}

function finiteNumber(value) {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

export function sanitizeSelection(value) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    return undefined;
  }
  const output = {};
  const boundary = value.boundary;
  if (boundary !== null && typeof boundary === "object" && !Array.isArray(boundary)) {
    const x = finiteNumber(boundary.x);
    const y = finiteNumber(boundary.y);
    const width = finiteNumber(boundary.width);
    const height = finiteNumber(boundary.height);
    if (
      x !== undefined &&
      y !== undefined &&
      width !== undefined &&
      height !== undefined &&
      x >= 0 &&
      y >= 0 &&
      width > 0 &&
      height > 0
    ) {
      output.boundary = { x, y, width, height };
    }
  }
  const selector = sanitizeSelector(value.selector);
  if (selector !== undefined) output.selector = selector;
  const tag = boundedText(value.tag, 32)?.toLowerCase();
  if (tag !== undefined && /^[a-z][a-z0-9-]*$/u.test(tag)) output.tag = tag;
  const role = boundedText(value.role, 64)?.toLowerCase();
  if (role !== undefined && IDENTIFIER.test(role)) output.role = role;
  const accessibleName = boundedText(value.accessibleName, 256);
  if (accessibleName !== undefined) output.accessibleName = accessibleName;
  const viewport = value.viewport;
  if (viewport !== null && typeof viewport === "object" && !Array.isArray(viewport)) {
    const width = finiteNumber(viewport.width);
    const height = finiteNumber(viewport.height);
    const devicePixelRatio = finiteNumber(viewport.devicePixelRatio);
    if (
      width !== undefined &&
      height !== undefined &&
      devicePixelRatio !== undefined &&
      width > 0 &&
      height > 0 &&
      devicePixelRatio > 0
    ) {
      output.viewport = { width, height, devicePixelRatio };
    }
  }
  if (
    output.boundary !== undefined &&
    output.viewport !== undefined &&
    (output.boundary.x + output.boundary.width > output.viewport.width ||
      output.boundary.y + output.boundary.height > output.viewport.height)
  ) {
    delete output.boundary;
  }
  return Object.keys(output).length === 0 ? undefined : output;
}

export function dataUrlImage(value) {
  if (typeof value !== "string") throw new Error("capture-unavailable");
  const match = /^data:image\/(png|jpeg);base64,([A-Za-z0-9+/]*={0,2})$/u.exec(value);
  if (match === null || match[1] === undefined || match[2] === undefined) {
    throw new Error("unsupported-image");
  }
  const base64 = match[2];
  const padding = base64.endsWith("==") ? 2 : base64.endsWith("=") ? 1 : 0;
  const encodedBytes = Math.floor((base64.length * 3) / 4) - padding;
  if (encodedBytes > MAX_ENCODED_IMAGE_BYTES) throw new Error("image-too-large");
  return { mediaType: match[1], base64, encodedBytes };
}

export function jsonByteLength(value) {
  return new TextEncoder().encode(JSON.stringify(value)).byteLength;
}

export function assertExtensionMessageSize(value) {
  if (jsonByteLength(value) >= MAX_EXTENSION_MESSAGE_BYTES) {
    throw new Error("message-too-large");
  }
}

export function assertHostResponseSize(value) {
  if (jsonByteLength(value) >= MAX_HOST_RESPONSE_BYTES) {
    throw new Error("host-response-too-large");
  }
}

export function captureRequest(draft) {
  const request = {
    version: PROTOCOL_VERSION,
    kind: "submit-capture",
    requestId: crypto.randomUUID(),
    captureMode: draft.captureMode,
  };
  const url = sanitizePageUrl(draft.url);
  const title = sanitizePageTitle(draft.title);
  if (url !== undefined || title !== undefined) {
    request.page = {};
    if (url !== undefined) request.page.url = url;
    if (title !== undefined) request.page.title = title;
  }
  if (draft.captureMode === "visible-viewport") {
    if (draft.image === undefined) throw new Error("capture-unavailable");
    request.image = draft.image;
  } else if (draft.captureMode !== "os-capture") {
    throw new Error("invalid-capture-mode");
  }
  const selection = sanitizeSelection(draft.selection);
  if (selection !== undefined && draft.captureMode === "visible-viewport") {
    request.selection = selection;
  }
  assertExtensionMessageSize(request);
  return request;
}
