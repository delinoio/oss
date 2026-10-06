import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

// Vitest exposes jsdom globals through Node's global object. Node 26 also
// exposes a placeholder `localStorage` global, so the normal jsdom storage
// accessors are not copied onto `window`. Use jsdom's origin-scoped storage so
// browser-facing code observes the same storage behavior in component tests.
const jsdomWindow = (globalThis as typeof globalThis & { jsdom?: { window: Window } }).jsdom?.window;
if (jsdomWindow) {
  Object.defineProperty(window, "localStorage", { configurable: true, value: jsdomWindow.localStorage });
  Object.defineProperty(window, "sessionStorage", { configurable: true, value: jsdomWindow.sessionStorage });
}

// jsdom lacks native modal behavior. Production uses the browser's dialog,
// while component fixtures emulate only opening/closing for lifecycle checks.
HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); };
HTMLDialogElement.prototype.close = function () { this.removeAttribute("open"); };
afterEach(cleanup);
