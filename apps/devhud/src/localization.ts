export type SupportedLanguage = "en" | "ko";

export type CopyKey = keyof typeof messages.en;

export const messages = {
  en: {
    appName: "DevHUD",
    home: "Home", realqa: "RealQA", deck: "Deck", settings: "Settings",
    account: "Account", diagnostics: "Diagnostics", commandPalette: "Command palette",
    searchCommands: "Search commands", noCommands: "No available commands.",
    openPalette: "Open command palette", close: "Close", quit: "Quit DevHUD",
    showWindow: "Show DevHUD", theme: "Theme", language: "Language",
    system: "System", light: "Light", dark: "Dark", english: "English", korean: "Korean",
    launchAtLogin: "Launch DevHUD when you sign in", launchAtLoginHint: "Off by default. You can change this any time.",
    welcome: "A calm home for your developer work", homeSummary: "Open a first-party tool or use the command palette to move quickly.",
    available: "Available", unavailable: "Unavailable on this platform", planned: "This workflow will be available when its native or service integration is connected.",
    realqaTitle: "Capture better bug reports", realqaSummary: "Capture actions are ready to discover. Native capture is not connected in this local shell.",
    deckTitle: "Keep pull requests in view", deckSummary: "Deck is ready for local navigation. GitHub data refresh is not connected yet.",
    accountTitle: "Account and API", accountSummary: "Use DevHUD locally, or prepare an official API endpoint for a future sign-in.",
    apiOrigin: "API origin", apiOriginHint: "HTTPS is required except for loopback development endpoints.",
    signIn: "Open sign in", continueLocally: "Continue locally", pat: "Create a GitHub PAT", docs: "Open documentation", issue: "Report an issue",
    diagnosticsTitle: "Diagnostics", diagnosticsSummary: "Redacted host diagnostics stay local until crash reporting is explicitly enabled.",
    diagnosticsEmpty: "No diagnostics have been recorded in this shell session.",
    saved: "Saved locally", settingsTitle: "Preferences", settingsSummary: "These display preferences are stored only on this device.",
    captureDisplay: "Capture display", captureWindow: "Capture active window", captureAll: "Capture all displays", captureSelection: "Capture selection", captureToolbar: "Open capture toolbar",
    navHome: "Go to Home", navRealqa: "Open RealQA", navDeck: "Open Deck", navSettings: "Open Settings", navAccount: "Open Account", navDiagnostics: "Open Diagnostics",
    settingTheme: "Set theme", settingLanguage: "Set language", settingLaunch: "Toggle launch at login",
    externalOpened: "Opened in your system browser.",
  },
  ko: {
    appName: "DevHUD",
    home: "홈", realqa: "RealQA", deck: "Deck", settings: "설정",
    account: "계정", diagnostics: "진단", commandPalette: "명령 팔레트",
    searchCommands: "명령 검색", noCommands: "사용 가능한 명령이 없습니다.",
    openPalette: "명령 팔레트 열기", close: "닫기", quit: "DevHUD 종료",
    showWindow: "DevHUD 표시", theme: "테마", language: "언어",
    system: "시스템", light: "라이트", dark: "다크", english: "영어", korean: "한국어",
    launchAtLogin: "로그인할 때 DevHUD 실행", launchAtLoginHint: "기본값은 꺼짐이며 언제든 변경할 수 있습니다.",
    welcome: "개발 작업을 위한 차분한 공간", homeSummary: "첫 번째 도구를 열거나 명령 팔레트로 빠르게 이동하세요.",
    available: "사용 가능", unavailable: "이 플랫폼에서 사용할 수 없음", planned: "네이티브 또는 서비스 통합이 연결되면 이 워크플로를 사용할 수 있습니다.",
    realqaTitle: "더 나은 버그 리포트 캡처", realqaSummary: "캡처 명령을 찾을 수 있습니다. 이 로컬 셸에는 네이티브 캡처가 아직 연결되지 않았습니다.",
    deckTitle: "풀 리퀘스트 한눈에 보기", deckSummary: "Deck의 로컬 탐색을 사용할 수 있습니다. GitHub 데이터 새로 고침은 아직 연결되지 않았습니다.",
    accountTitle: "계정 및 API", accountSummary: "DevHUD를 로컬에서 사용하거나 향후 로그인을 위해 공식 API 엔드포인트를 준비하세요.",
    apiOrigin: "API 원본", apiOriginHint: "루프백 개발 엔드포인트를 제외하면 HTTPS가 필요합니다.",
    signIn: "로그인 열기", continueLocally: "로컬에서 계속", pat: "GitHub PAT 만들기", docs: "문서 열기", issue: "이슈 신고",
    diagnosticsTitle: "진단", diagnosticsSummary: "삭제된 호스트 진단은 크래시 보고를 명시적으로 활성화하기 전까지 로컬에 유지됩니다.",
    diagnosticsEmpty: "이 셸 세션에는 기록된 진단이 없습니다.",
    saved: "로컬에 저장됨", settingsTitle: "환경설정", settingsSummary: "표시 환경설정은 이 장치에만 저장됩니다.",
    captureDisplay: "디스플레이 캡처", captureWindow: "활성 창 캡처", captureAll: "모든 디스플레이 캡처", captureSelection: "선택 영역 캡처", captureToolbar: "캡처 도구 모음 열기",
    navHome: "홈으로 이동", navRealqa: "RealQA 열기", navDeck: "Deck 열기", navSettings: "설정 열기", navAccount: "계정 열기", navDiagnostics: "진단 열기",
    settingTheme: "테마 설정", settingLanguage: "언어 설정", settingLaunch: "로그인 시 실행 전환",
    externalOpened: "시스템 브라우저에서 열었습니다.",
  },
} as const;

export function selectSupportedLanguage(languages: readonly string[]): SupportedLanguage {
  for (const locale of languages) {
    const language = locale.trim().toLowerCase().split(/[-_]/u, 1)[0];
    if (language === "en" || language === "ko") return language;
  }
  return "en";
}
