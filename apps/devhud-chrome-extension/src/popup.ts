import { resolveExtensionLanguage } from "./popup-language.js";

interface Configuration {
  readonly origins?: readonly { readonly origin: string; readonly mappings: readonly unknown[] }[];
  readonly language?: "en" | "ko";
}

const status = document.querySelector<HTMLOutputElement>("#status")!;
const originList = document.querySelector<HTMLUListElement>("#origins")!;
const pairingInput = document.querySelector<HTMLInputElement>("#pairing-nonce")!;
document.documentElement.lang = resolveExtensionLanguage(chrome.i18n.getUILanguage());

function text(id: string): string {
  return chrome.i18n.getMessage(id) || id;
}

function announce(message: string, error = false) {
  status.textContent = message;
  status.dataset.error = error ? "true" : "false";
}

async function send(message: unknown): Promise<{ ok?: boolean; state?: string; payload?: unknown }> {
  return await chrome.runtime.sendMessage(message) as { ok?: boolean; state?: string; payload?: unknown };
}

function validConfiguredOrigin(value: string): boolean {
  try {
    const url = new URL(value);
    return (url.protocol === "http:" || url.protocol === "https:") && url.origin === value && !url.username && !url.password;
  } catch { return false; }
}

function renderOrigins(configuration: Configuration) {
  originList.replaceChildren();
  for (const configured of configuration.origins ?? []) {
    if (!validConfiguredOrigin(configured.origin)) continue;
    const item = document.createElement("li");
    const label = document.createElement("code");
    label.textContent = configured.origin;
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = text("allowOrigin");
    button.dataset.origin = configured.origin;
    button.addEventListener("click", () => {
      const origin = button.dataset.origin;
      if (!origin || !validConfiguredOrigin(origin)) { announce(text("permissionDenied"), true); return; }
      // This call intentionally occurs synchronously inside the button gesture.
      void chrome.permissions.request({ origins: [`${origin}/*`] }).then((granted) => announce(text(granted ? "permissionGranted" : "permissionDenied"), !granted));
    });
    item.append(label, button);
    originList.append(item);
  }
  if (!originList.childElementCount) {
    const item = document.createElement("li"); item.textContent = text("noConfiguredOrigins"); originList.append(item);
  }
}

async function refreshOrigins() {
  try {
    const response = await send({ type: "configuration" });
    renderOrigins((response.payload ?? {}) as Configuration);
  } catch {
    renderOrigins({});
  }
}

for (const element of document.querySelectorAll<HTMLElement>("[data-i18n]")) element.textContent = text(element.dataset.i18n!);

document.querySelector<HTMLButtonElement>("#pair")!.addEventListener("click", () => {
  const pairingNonce = pairingInput.value.trim();
  if (!pairingNonce) { announce(text("pairingRequired"), true); pairingInput.focus(); return; }
  void send({ type: "pair", pairingNonce }).then((response) => {
    announce(text(response.ok ? "paired" : "pairingFailed"), !response.ok);
    if (response.ok) void refreshOrigins();
  });
});

for (const [id, selectElement] of [["capture", false], ["select", true]] as const) document.querySelector<HTMLButtonElement>(`#${id}`)!.addEventListener("click", () => {
  void send({ type: "capture", selectElement }).then((response) => announce(text(response.ok ? "captureSent" : "manualSelection"), !response.ok));
});

void refreshOrigins();
pairingInput.focus();
