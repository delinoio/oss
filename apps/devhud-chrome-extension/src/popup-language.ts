export type ExtensionLanguage = "en" | "ko";

export function resolveExtensionLanguage(uiLanguage: string): ExtensionLanguage {
  return uiLanguage.toLowerCase().split("-")[0] === "ko" ? "ko" : "en";
}
