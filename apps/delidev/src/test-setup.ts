import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

// jsdom lacks native modal behavior. Production uses the browser's dialog,
// while component fixtures emulate only opening/closing for lifecycle checks.
HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); };
HTMLDialogElement.prototype.close = function () { this.removeAttribute("open"); };
afterEach(cleanup);
