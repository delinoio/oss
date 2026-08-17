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
    accountTitle: "Account and API", accountSummary: "Manage your authenticated session and selected DevHUD API.", firstRunSummary: "Sign in to synchronize non-secret settings, or continue with local guest settings.",
    apiOrigin: "API origin", apiOriginHint: "HTTPS is required except for loopback development endpoints. TLS verification cannot be disabled.", applyApiOrigin: "Apply API origin",
    customApiWarning: "A custom API controls authentication discovery and every synchronized non-secret setting. Trust it before signing in.", apiChangeConfirm: "Changing API signs you out of the current API and clears its tokens and cached authenticated data. Continue?",
    signIn: "Sign in", continueLocally: "Continue locally", pat: "Create a GitHub PAT", issue: "Report an issue", fetchingBootstrap: "Checking authentication configuration…", bootstrapFailed: "DevHUD could not validate this API's authentication configuration.", signInFailed: "DevHUD could not start secure sign in.", resetSignIn: "Reset secure sign in", resetSignInHint: "Resetting removes the unreadable sign-in session from this device. Synchronized settings remain on the server.",
    signedInSession: "Signed-in session", signedIn: "Signed in", loadingAccount: "Loading account details…", accountLoadFailed: "DevHUD could not load the account.", logout: "Log out", deleteAccount: "Delete account", cancel: "Cancel", deleteAccountConfirmTitle: "Schedule account deletion?", deleteAccountConfirmSummary: "Access is blocked immediately. Synchronized data is recoverable for 30 days; local PAT and R2 credentials are deleted now.", accountActionFailed: "DevHUD could not complete the account action.", deletionPendingTitle: "Account scheduled for deletion", deletionPendingSummary: "Authenticated service access is blocked. Restore within 30 days or log out and remove the recovery session.", recoverableUntil: "Recoverable until", restoreAccount: "Restore account",
    synchronizedSettings: "Synchronized non-secret settings", guestSettingsLocal: "Guest settings remain only on this device.", offlineSettingsReadOnly: "Cached synchronized settings are read-only while offline. Direct local features remain available.", settingsRevision: "Server revision", importSettingsTitle: "Choose which complete settings snapshot to keep", importSettingsSummary: "Guest and server settings are never merged automatically.", uploadLocal: "Upload local snapshot", replaceLocal: "Replace local with server", conflictTitle: "Settings changed on another device", conflictSummary: "Review the latest complete diff, then adopt the server or explicitly reapply your unchanged local snapshot.", reapplyLocal: "Reapply local snapshot", adoptServer: "Adopt server snapshot", settingsActionFailed: "DevHUD could not synchronize settings.", completeSnapshotDiff: "Complete redacted snapshot diff", settingPath: "Setting", localValue: "Local", serverValue: "Server", noDifferences: "The snapshots are identical.",
    diagnosticsTitle: "Diagnostics", diagnosticsSummary: "Redacted host diagnostics stay local until crash reporting is explicitly enabled.",
    diagnosticsUnavailable: "Host diagnostics are recorded locally and cannot be viewed in this shell yet.",
    saved: "Saved locally", settingsTitle: "Preferences", settingsSummary: "Appearance synchronizes when you sign in and stays local in guest mode.",
    captureDisplay: "Capture display", captureWindow: "Capture active window", captureAll: "Capture all displays", captureSelection: "Capture selection", captureToolbar: "Open capture toolbar",
    navHome: "Go to Home", navRealqa: "Open RealQA", navDeck: "Open Deck", navSettings: "Open Settings", navAccount: "Open Account", navDiagnostics: "Open Diagnostics",
    settingTheme: "Set theme", settingLanguage: "Set language", settingLaunch: "Toggle launch at login",
    keyboardShortcuts: "Keyboard shortcuts", shortcutEnabled: "Enabled", shortcutModifier: "Modifier", shortcutKey: "Key", shortcutNone: "None", shortcutRightPrimary: "Right primary modifier", shortcutShift: "Shift", shortcutAlt: "Alt", shortcutKeyK: "K", shortcutDigit1: "1", shortcutDigit2: "2", shortcutDigit3: "3", shortcutDigit4: "4", shortcutDigit5: "5", shortcutSpace: "Space", shortcutTab: "Tab", shortcutKeyQ: "Q", shortcutDelete: "Delete", shortcutBackspace: "Backspace", shortcutMalformed: "This shortcut is not a supported chord.", shortcutConflict: "This shortcut conflicts with another DevHUD action.", shortcutReserved: "This shortcut is reserved by the operating system.", shortcutRegistrationFailed: "DevHUD could not register this shortcut. Your previous binding is still active.", shortcutPermissionDenied: "DevHUD cannot monitor the configured shortcut until permission is granted.", shortcutMacAccessibility: "Allow DevHUD in Accessibility and Input Monitoring to use global right Command shortcuts.", shortcutMacInputMonitoring: "Open macOS Privacy & Security, then enable DevHUD under Input Monitoring.", shortcutLinuxGuidance: "Global shortcuts require an X11 or XWayland session with DISPLAY and your user X authority. Native Wayland is unsupported.", shortcutRequestPermission: "Check shortcut permission", shortcutUnsupported: "Global desktop shortcuts are unavailable on this platform.",
    externalOpened: "Opened in your system browser.", externalFailed: "DevHUD could not open your system browser.", invalidApiOrigin: "Enter a valid HTTPS or loopback HTTP API origin.",
    loading: "Loading", loadingTitle: "Preparing DevHUD", loadingSummary: "Checking this device and its available native capabilities.",
    empty: "Empty", emptyTitle: "Nothing here yet", emptySummary: "Create a Deck when the GitHub integration is connected.",
    offline: "Offline", offlineTitle: "You are offline", offlineSummary: "Cached local information remains available. Synchronized settings stay read-only until the connection returns.", lastSuccessfulRefresh: "Last successful refresh",
    blocked: "Limited", blockedTitle: "Official services are unavailable", blockedSummary: "This account cannot use authenticated DevHUD API operations.", blockedLocalHint: "Guest mode and local-only behavior remain available.",
    error: "Error", errorTitle: "DevHUD could not finish that action", errorSummary: "No sensitive values were included in this message. Try again or review local diagnostics.", retry: "Try again", correlationId: "Correlation ID",
    runtimeReady: "Native capabilities ready.", runtimeFailed: "Native capabilities could not be loaded.",
    mobileNavigation: "Primary navigation", desktopOnly: "Desktop only", notificationPermission: "Notification permission", notificationNotDetermined: "Not determined", notificationDenied: "Denied", notificationAuthorized: "Authorized", notificationPermissionFailed: "DevHUD could not request notification permission. Try again.", updatePolicy: "Updates are managed by your app store.", storeOpenFailed: "DevHUD could not open your app store. Try again.",
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
    accountTitle: "계정 및 API", accountSummary: "인증 세션과 선택한 DevHUD API를 관리하세요.", firstRunSummary: "로그인하여 비밀이 아닌 설정을 동기화하거나 로컬 게스트 설정으로 계속하세요.",
    apiOrigin: "API 원본", apiOriginHint: "루프백 개발 엔드포인트를 제외하면 HTTPS가 필요합니다. TLS 검증은 끌 수 없습니다.", applyApiOrigin: "API 원본 적용",
    customApiWarning: "사용자 지정 API는 인증 검색과 동기화되는 모든 비밀이 아닌 설정을 제어합니다. 로그인하기 전에 신뢰할 수 있는지 확인하세요.", apiChangeConfirm: "API를 변경하면 현재 API에서 로그아웃되고 해당 토큰과 인증 캐시가 삭제됩니다. 계속할까요?",
    signIn: "로그인", continueLocally: "로컬에서 계속", pat: "GitHub PAT 만들기", issue: "이슈 신고", fetchingBootstrap: "인증 구성을 확인하는 중…", bootstrapFailed: "이 API의 인증 구성을 검증할 수 없습니다.", signInFailed: "안전한 로그인을 시작할 수 없습니다.", resetSignIn: "안전한 로그인 초기화", resetSignInHint: "초기화하면 이 기기에서 읽을 수 없는 로그인 세션이 삭제됩니다. 동기화된 설정은 서버에 유지됩니다.",
    signedInSession: "로그인 세션", signedIn: "로그인됨", loadingAccount: "계정 정보를 불러오는 중…", accountLoadFailed: "DevHUD에서 계정을 불러오지 못했습니다.", logout: "로그아웃", deleteAccount: "계정 삭제", cancel: "취소", deleteAccountConfirmTitle: "계정 삭제를 예약할까요?", deleteAccountConfirmSummary: "접근은 즉시 차단됩니다. 동기화 데이터는 30일 동안 복구할 수 있으며 로컬 PAT와 R2 자격 증명은 지금 삭제됩니다.", accountActionFailed: "계정 작업을 완료할 수 없습니다.", deletionPendingTitle: "계정 삭제 예약됨", deletionPendingSummary: "인증 서비스 접근이 차단되었습니다. 30일 안에 복원하거나 로그아웃하여 복구 세션을 삭제하세요.", recoverableUntil: "복구 가능 기한", restoreAccount: "계정 복원",
    synchronizedSettings: "동기화된 비밀이 아닌 설정", guestSettingsLocal: "게스트 설정은 이 장치에만 유지됩니다.", offlineSettingsReadOnly: "오프라인에서는 캐시된 동기화 설정이 읽기 전용입니다. 직접 로컬 기능은 계속 사용할 수 있습니다.", settingsRevision: "서버 리비전", importSettingsTitle: "유지할 전체 설정 스냅샷 선택", importSettingsSummary: "게스트 설정과 서버 설정은 자동으로 병합되지 않습니다.", uploadLocal: "로컬 스냅샷 업로드", replaceLocal: "로컬을 서버 설정으로 교체", conflictTitle: "다른 장치에서 설정이 변경됨", conflictSummary: "최신 전체 차이를 검토한 후 서버 설정을 채택하거나 변경되지 않은 로컬 스냅샷을 명시적으로 다시 적용하세요.", reapplyLocal: "로컬 스냅샷 다시 적용", adoptServer: "서버 스냅샷 채택", settingsActionFailed: "설정을 동기화할 수 없습니다.", completeSnapshotDiff: "비밀이 제거된 전체 스냅샷 차이", settingPath: "설정", localValue: "로컬", serverValue: "서버", noDifferences: "스냅샷이 같습니다.",
    diagnosticsTitle: "진단", diagnosticsSummary: "민감 정보가 삭제된 호스트 진단은 크래시 보고를 명시적으로 활성화하기 전까지 로컬에 유지됩니다.",
    diagnosticsUnavailable: "호스트 진단은 로컬에 기록되며 아직 이 셸에서 볼 수 없습니다.",
    saved: "로컬에 저장됨", settingsTitle: "환경설정", settingsSummary: "로그인하면 표시 설정이 동기화되고 게스트 모드에서는 로컬에 저장됩니다.",
    captureDisplay: "디스플레이 캡처", captureWindow: "활성 창 캡처", captureAll: "모든 디스플레이 캡처", captureSelection: "선택 영역 캡처", captureToolbar: "캡처 도구 모음 열기",
    navHome: "홈으로 이동", navRealqa: "RealQA 열기", navDeck: "Deck 열기", navSettings: "설정 열기", navAccount: "계정 열기", navDiagnostics: "진단 열기",
    settingTheme: "테마 설정", settingLanguage: "언어 설정", settingLaunch: "로그인 시 실행 전환",
    keyboardShortcuts: "키보드 단축키", shortcutEnabled: "사용", shortcutModifier: "보조 키", shortcutKey: "키", shortcutNone: "없음", shortcutRightPrimary: "오른쪽 기본 보조 키", shortcutShift: "Shift", shortcutAlt: "Alt", shortcutKeyK: "K", shortcutDigit1: "1", shortcutDigit2: "2", shortcutDigit3: "3", shortcutDigit4: "4", shortcutDigit5: "5", shortcutSpace: "스페이스", shortcutTab: "Tab", shortcutKeyQ: "Q", shortcutDelete: "Delete", shortcutBackspace: "Backspace", shortcutMalformed: "이 단축키는 지원되는 조합이 아닙니다.", shortcutConflict: "이 단축키는 다른 DevHUD 작업과 충돌합니다.", shortcutReserved: "이 단축키는 운영 체제에서 예약되어 있습니다.", shortcutRegistrationFailed: "DevHUD에서 이 단축키를 등록할 수 없습니다. 이전 단축키는 계속 활성화되어 있습니다.", shortcutPermissionDenied: "권한이 부여될 때까지 DevHUD에서 설정한 단축키를 감지할 수 없습니다.", shortcutMacAccessibility: "전역 오른쪽 Command 단축키를 사용하려면 손쉬운 사용 및 입력 모니터링에서 DevHUD를 허용하세요.", shortcutMacInputMonitoring: "macOS 개인정보 보호 및 보안을 열고 입력 모니터링에서 DevHUD를 활성화하세요.", shortcutLinuxGuidance: "전역 단축키에는 DISPLAY 및 사용자 X 권한이 있는 X11 또는 XWayland 세션이 필요합니다. 네이티브 Wayland는 지원되지 않습니다.", shortcutRequestPermission: "단축키 권한 확인", shortcutUnsupported: "이 플랫폼에서는 전역 데스크톱 단축키를 사용할 수 없습니다.",
    externalOpened: "시스템 브라우저에서 열었습니다.", externalFailed: "DevHUD에서 시스템 브라우저를 열 수 없습니다.", invalidApiOrigin: "유효한 HTTPS 또는 루프백 HTTP API 원본을 입력하세요.",
    loading: "불러오는 중", loadingTitle: "DevHUD 준비 중", loadingSummary: "이 장치와 사용 가능한 네이티브 기능을 확인하고 있습니다.",
    empty: "비어 있음", emptyTitle: "아직 표시할 항목이 없습니다", emptySummary: "GitHub 통합이 연결되면 Deck을 만들 수 있습니다.",
    offline: "오프라인", offlineTitle: "오프라인 상태입니다", offlineSummary: "캐시된 로컬 정보는 계속 사용할 수 있습니다. 연결이 복구될 때까지 동기화 설정은 읽기 전용입니다.", lastSuccessfulRefresh: "마지막으로 성공한 새로 고침",
    blocked: "제한됨", blockedTitle: "공식 서비스를 사용할 수 없습니다", blockedSummary: "이 계정은 인증된 DevHUD API 작업을 사용할 수 없습니다.", blockedLocalHint: "게스트 모드와 로컬 전용 기능은 계속 사용할 수 있습니다.",
    error: "오류", errorTitle: "DevHUD에서 작업을 완료하지 못했습니다", errorSummary: "이 메시지에는 민감한 값이 포함되지 않았습니다. 다시 시도하거나 로컬 진단을 확인하세요.", retry: "다시 시도", correlationId: "상관관계 ID",
    runtimeReady: "네이티브 기능이 준비되었습니다.", runtimeFailed: "네이티브 기능을 불러오지 못했습니다.",
    mobileNavigation: "기본 탐색", desktopOnly: "데스크톱 전용", notificationPermission: "알림 권한", notificationNotDetermined: "결정되지 않음", notificationDenied: "거부됨", notificationAuthorized: "허용됨", notificationPermissionFailed: "DevHUD에서 알림 권한을 요청하지 못했습니다. 다시 시도하세요.", updatePolicy: "업데이트는 앱 스토어에서 관리합니다.", storeOpenFailed: "DevHUD에서 앱 스토어를 열지 못했습니다. 다시 시도하세요.",
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
