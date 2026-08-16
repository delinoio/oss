export type SupportedLanguage = "en" | "ko";

export type CopyKey = keyof typeof messages.en;
export type Copy = (typeof messages)[SupportedLanguage];

export const messages = {
  en: {
    appName: "DevHUD",
    home: "Home", realqa: "RealQA", deck: "Deck", settings: "Settings",
    account: "Account", diagnostics: "Diagnostics", commandPalette: "Command palette",
    searchCommands: "Search commands", noCommands: "No available commands.",
    openPalette: "Open command palette", rightCommandK: "Right ⌘ K", rightControlK: "Right Ctrl K", close: "Close", quit: "Quit DevHUD",
    showWindow: "Show DevHUD", theme: "Theme", language: "Language",
    system: "System", light: "Light", dark: "Dark", english: "English", korean: "Korean",
    launchAtLogin: "Launch DevHUD when you sign in", launchAtLoginHint: "Off by default. You can change this any time.",
    welcome: "A calm home for your developer work", homeSummary: "Open a first-party tool or use the command palette to move quickly.",
    available: "Available", unavailable: "Unavailable on this platform", planned: "This workflow will be available when its native or service integration is connected.",
    realqaTitle: "Capture better bug reports", realqaSummary: "Capture actions are ready to discover. Native capture is not connected in this local shell.",
    realqaMobileTitle: "RealQA stays on desktop", realqaMobileSummary: "Mobile DevHUD does not capture screens or create issues. Use the desktop app for the complete RealQA workflow.",
    deckTitle: "Keep pull requests in view", deckSummary: "Deck is ready for local navigation. GitHub data refresh is not connected yet.",
    accountTitle: "Account and API", accountSummary: "Use DevHUD locally, or prepare an official API endpoint for a future sign-in.",
    apiOrigin: "API origin", apiOriginHint: "HTTPS is required except for loopback development endpoints.",
    signIn: "Open sign in", continueLocally: "Continue locally", pat: "Create a GitHub PAT", issue: "Report an issue",
    diagnosticsTitle: "Diagnostics", diagnosticsSummary: "Redacted host diagnostics stay local until crash reporting is explicitly enabled.",
    diagnosticsUnavailable: "Host diagnostics are recorded locally and cannot be viewed in this shell yet.",
    saved: "Saved locally", settingsTitle: "Preferences", settingsSummary: "These display preferences are stored only on this device.",
    captureDisplay: "Capture display", captureWindow: "Capture active window", captureAll: "Capture all displays", captureSelection: "Capture selection", captureToolbar: "Open capture toolbar",
    navHome: "Go to Home", navRealqa: "Open RealQA", navDeck: "Open Deck", navSettings: "Open Settings", navAccount: "Open Account", navDiagnostics: "Open Diagnostics",
    settingTheme: "Set theme", settingLanguage: "Set language", settingLaunch: "Toggle launch at login",
    externalOpened: "Opened in your system browser.", externalFailed: "DevHUD could not open your system browser.", invalidApiOrigin: "Enter a valid HTTPS or loopback HTTP API origin.",
    loading: "Loading", loadingTitle: "Preparing DevHUD", loadingSummary: "Checking this device and its available native capabilities.",
    empty: "Empty", emptyTitle: "Nothing here yet", emptySummary: "Create a Deck when the GitHub integration is connected.",
    offline: "Offline", offlineTitle: "You are offline", offlineSummary: "Cached local information remains available. Synchronized settings stay read-only until the connection returns.", lastSuccessfulRefresh: "Last successful refresh",
    blocked: "Limited", blockedTitle: "Official services are unavailable", blockedSummary: "This account cannot use authenticated DevHUD API operations.", blockedLocalHint: "Guest mode and local-only behavior remain available.",
    error: "Error", errorTitle: "DevHUD could not finish that action", errorSummary: "No sensitive values were included in this message. Try again or review local diagnostics.", retry: "Try again", correlationId: "Correlation ID",
    runtimeReady: "Native capabilities ready.", runtimeFailed: "Native capabilities could not be loaded.",
    mobileNavigation: "Primary navigation", desktopOnly: "Desktop only", notificationPermission: "Notification permission", notificationNotDetermined: "Not determined", notificationDenied: "Denied", notificationAuthorized: "Authorized", updatePolicy: "Updates are managed by your app store.",
    diagnosticPlatform: "Platform", diagnosticArchitecture: "Architecture", diagnosticBridge: "Bridge",
  },
  ko: {
    appName: "DevHUD",
    home: "홈", realqa: "RealQA", deck: "Deck", settings: "설정",
    account: "계정", diagnostics: "진단", commandPalette: "명령 팔레트",
    searchCommands: "명령 검색", noCommands: "사용 가능한 명령이 없습니다.",
    openPalette: "명령 팔레트 열기", rightCommandK: "오른쪽 ⌘ K", rightControlK: "오른쪽 Ctrl K", close: "닫기", quit: "DevHUD 종료",
    showWindow: "DevHUD 표시", theme: "테마", language: "언어",
    system: "시스템", light: "라이트", dark: "다크", english: "영어", korean: "한국어",
    launchAtLogin: "로그인할 때 DevHUD 실행", launchAtLoginHint: "기본값은 꺼짐이며 언제든 변경할 수 있습니다.",
    welcome: "개발 작업을 위한 차분한 공간", homeSummary: "첫 번째 도구를 열거나 명령 팔레트로 빠르게 이동하세요.",
    available: "사용 가능", unavailable: "이 플랫폼에서 사용할 수 없음", planned: "네이티브 또는 서비스 통합이 연결되면 이 워크플로를 사용할 수 있습니다.",
    realqaTitle: "더 나은 버그 리포트 캡처", realqaSummary: "캡처 명령을 찾을 수 있습니다. 이 로컬 셸에는 네이티브 캡처가 아직 연결되지 않았습니다.",
    realqaMobileTitle: "RealQA는 데스크톱에서 사용하세요", realqaMobileSummary: "모바일 DevHUD는 화면을 캡처하거나 이슈를 만들지 않습니다. 전체 RealQA 워크플로는 데스크톱 앱에서 사용하세요.",
    deckTitle: "풀 리퀘스트 한눈에 보기", deckSummary: "Deck의 로컬 탐색을 사용할 수 있습니다. GitHub 데이터 새로 고침은 아직 연결되지 않았습니다.",
    accountTitle: "계정 및 API", accountSummary: "DevHUD를 로컬에서 사용하거나 향후 로그인을 위해 공식 API 엔드포인트를 준비하세요.",
    apiOrigin: "API 원본", apiOriginHint: "루프백 개발 엔드포인트를 제외하면 HTTPS가 필요합니다.",
    signIn: "로그인 열기", continueLocally: "로컬에서 계속", pat: "GitHub PAT 만들기", issue: "이슈 신고",
    diagnosticsTitle: "진단", diagnosticsSummary: "민감 정보가 삭제된 호스트 진단은 크래시 보고를 명시적으로 활성화하기 전까지 로컬에 유지됩니다.",
    diagnosticsUnavailable: "호스트 진단은 로컬에 기록되며 아직 이 셸에서 볼 수 없습니다.",
    saved: "로컬에 저장됨", settingsTitle: "환경설정", settingsSummary: "표시 환경설정은 이 장치에만 저장됩니다.",
    captureDisplay: "디스플레이 캡처", captureWindow: "활성 창 캡처", captureAll: "모든 디스플레이 캡처", captureSelection: "선택 영역 캡처", captureToolbar: "캡처 도구 모음 열기",
    navHome: "홈으로 이동", navRealqa: "RealQA 열기", navDeck: "Deck 열기", navSettings: "설정 열기", navAccount: "계정 열기", navDiagnostics: "진단 열기",
    settingTheme: "테마 설정", settingLanguage: "언어 설정", settingLaunch: "로그인 시 실행 전환",
    externalOpened: "시스템 브라우저에서 열었습니다.", externalFailed: "DevHUD에서 시스템 브라우저를 열 수 없습니다.", invalidApiOrigin: "유효한 HTTPS 또는 루프백 HTTP API 원본을 입력하세요.",
    loading: "불러오는 중", loadingTitle: "DevHUD 준비 중", loadingSummary: "이 장치와 사용 가능한 네이티브 기능을 확인하고 있습니다.",
    empty: "비어 있음", emptyTitle: "아직 표시할 항목이 없습니다", emptySummary: "GitHub 통합이 연결되면 Deck을 만들 수 있습니다.",
    offline: "오프라인", offlineTitle: "오프라인 상태입니다", offlineSummary: "캐시된 로컬 정보는 계속 사용할 수 있습니다. 연결이 복구될 때까지 동기화 설정은 읽기 전용입니다.", lastSuccessfulRefresh: "마지막으로 성공한 새로 고침",
    blocked: "제한됨", blockedTitle: "공식 서비스를 사용할 수 없습니다", blockedSummary: "이 계정은 인증된 DevHUD API 작업을 사용할 수 없습니다.", blockedLocalHint: "게스트 모드와 로컬 전용 기능은 계속 사용할 수 있습니다.",
    error: "오류", errorTitle: "DevHUD에서 작업을 완료하지 못했습니다", errorSummary: "이 메시지에는 민감한 값이 포함되지 않았습니다. 다시 시도하거나 로컬 진단을 확인하세요.", retry: "다시 시도", correlationId: "상관관계 ID",
    runtimeReady: "네이티브 기능이 준비되었습니다.", runtimeFailed: "네이티브 기능을 불러오지 못했습니다.",
    mobileNavigation: "기본 탐색", desktopOnly: "데스크톱 전용", notificationPermission: "알림 권한", notificationNotDetermined: "결정되지 않음", notificationDenied: "거부됨", notificationAuthorized: "허용됨", updatePolicy: "업데이트는 앱 스토어에서 관리합니다.",
    diagnosticPlatform: "플랫폼", diagnosticArchitecture: "아키텍처", diagnosticBridge: "브리지",
  },
} as const;

export function selectSupportedLanguage(languages: readonly string[]): SupportedLanguage {
  for (const locale of languages) {
    const language = locale.trim().toLowerCase().split(/[-_]/u, 1)[0];
    if (language === "en" || language === "ko") return language;
  }
  return "en";
}
