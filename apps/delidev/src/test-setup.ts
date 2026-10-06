import { afterEach, beforeEach } from "vitest";
import { cleanup } from "@testing-library/react";
import { i18n, SupportedLanguage } from "./localization";

// Tests choose their language explicitly, independent of the validation host.
beforeEach(async () => { await i18n.changeLanguage(SupportedLanguage.English); document.documentElement.lang = "en"; });

// jsdom lacks native modal behavior. Production uses the browser's dialog,
// while component fixtures emulate only opening/closing for lifecycle checks.
HTMLDialogElement.prototype.showModal = function () { this.setAttribute("open", ""); };
HTMLDialogElement.prototype.close = function () { this.removeAttribute("open"); };
afterEach(cleanup);
