export type SupportedLanguage = "en" | "ko";

interface ShellCopy {
  eyebrow: string;
  summary: string;
}

export const shellCopy: Record<SupportedLanguage, ShellCopy> = {
  en: {
    eyebrow: "Desktop foundation",
    summary: "The pinned CEF desktop host is ready for the first DevHUD mini-apps.",
  },
  ko: {
    eyebrow: "데스크톱 기반",
    summary: "고정된 CEF 데스크톱 호스트가 첫 DevHUD 미니 앱을 위한 준비를 마쳤습니다.",
  },
};

export function selectSupportedLanguage(languages: readonly string[]): SupportedLanguage {
  for (const locale of languages) {
    const language = locale.trim().toLowerCase().split(/[-_]/u, 1)[0];
    if (language === "en" || language === "ko") {
      return language;
    }
  }
  return "en";
}
