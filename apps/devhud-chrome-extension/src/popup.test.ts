// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";

describe("popup pairing", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
    document.body.replaceChildren();
  });

  it("refreshes configured origins after pairing succeeds", async () => {
    document.body.innerHTML = `
      <input id="pairing-nonce">
      <button id="pair" type="button"></button>
      <button id="capture" type="button"></button>
      <button id="select" type="button"></button>
      <ul id="origins"></ul>
      <output id="status"></output>
    `;
    const sendMessage = vi.fn()
      .mockResolvedValueOnce({ ok: false, state: "unauthorized", payload: null })
      .mockResolvedValueOnce({ ok: true, state: "paired", payload: null })
      .mockResolvedValueOnce({
        ok: true,
        state: "configured",
        payload: { origins: [{ origin: "https://example.com", mappings: [] }] },
    });
    const requestPermission = vi.fn().mockResolvedValue(true);
    vi.stubGlobal("chrome", {
      i18n: {
        getUILanguage: () => "en-US",
        getMessage: (id: string, substitutions?: string | string[]) => id === "allowOriginFor" ? `${id} ${String(substitutions)}` : id,
      },
      permissions: {
        request: requestPermission,
      },
      runtime: { sendMessage },
    });

    await import("./popup.js");
    await vi.waitFor(() => expect(document.querySelector("#origins")?.textContent).toBe("noConfiguredOrigins"));

    const pairingInput = document.querySelector<HTMLInputElement>("#pairing-nonce")!;
    pairingInput.value = "pairing-nonce";
    document.querySelector<HTMLButtonElement>("#pair")!.click();

    await vi.waitFor(() => expect(document.querySelector("#origins")?.textContent).toContain("https://example.com"));
    expect(sendMessage).toHaveBeenNthCalledWith(1, { type: "configuration" });
    expect(sendMessage).toHaveBeenNthCalledWith(2, { type: "pair", pairingNonce: "pairing-nonce" });
    expect(sendMessage).toHaveBeenNthCalledWith(3, { type: "configuration" });
    expect(document.querySelector("#origins button")?.textContent).toBe("allowOrigin");
    expect(document.querySelector("#origins button")?.getAttribute("aria-label")).toBe("allowOriginFor https://example.com");
    expect(document.querySelector("#status")?.textContent).toBe("paired");

    document.querySelector<HTMLButtonElement>("#origins button")!.click();
    expect(requestPermission).toHaveBeenCalledWith({ origins: ["https://example.com:443/*"] });
  });

  it("includes each configured origin in its permission button name", async () => {
    document.body.innerHTML = `
      <input id="pairing-nonce">
      <button id="pair" type="button"></button>
      <button id="capture" type="button"></button>
      <button id="select" type="button"></button>
      <ul id="origins"></ul>
      <output id="status"></output>
    `;
    vi.stubGlobal("chrome", {
      i18n: {
        getUILanguage: () => "en-US",
        getMessage: (id: string, substitutions?: string | string[]) => id === "allowOriginFor" ? `Allow access to ${String(substitutions)}` : id,
      },
      permissions: {
        request: vi.fn().mockResolvedValue(true),
      },
      runtime: { sendMessage: vi.fn().mockResolvedValue({
        ok: true,
        state: "configured",
        payload: { origins: [
          { origin: "https://first.example", mappings: [] },
          { origin: "https://second.example", mappings: [] },
        ] },
      }) },
    });

    await import("./popup.js");

    await vi.waitFor(() => expect(document.querySelectorAll("#origins button")).toHaveLength(2));
    expect([...document.querySelectorAll("#origins button")].map((button) => button.getAttribute("aria-label"))).toEqual([
      "Allow access to https://first.example",
      "Allow access to https://second.example",
    ]);
  });
});
