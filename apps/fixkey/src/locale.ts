export const messages = {
  en: {
    general: "General",
    exclusions: "Excluded Apps",
    shortcuts: "Shortcuts",
    updates: "Updates",
    diagnostics: "Diagnostics and Help",
    tagline: "Keep typing. Fix the layout.",
    correction: "Automatic correction",
    pause: "Pause",
    resume: "Resume",
    languages: "Enabled languages",
    korean: "Korean · Dubeolsik",
    english: "English · US QWERTY",
    startup: "Start at login",
    uiLanguage: "UI language",
    theme: "Theme",
    system: "System",
    light: "Light",
    dark: "Dark",
    loading: "Connecting to FixKey…",
    connection: "Cannot connect to the desktop app.",
    retry: "Try again",
    active: "Correction is active",
    inactive: "Correction is inactive",
    waitingField: "Waiting for an editable text field",
    waitingFieldHelp:
      "Click an editable text field in another app. FixKey waits while Settings is open or the focused field cannot be safely identified. Details are available in Diagnostics and Help.",
    local: "Your input stays on this device. Password fields are excluded.",
    empty: "No excluded apps",
    emptyDetail:
      "Correction can run in supported editable controls. Add an app to always leave its input unchanged.",
    add: "Add app",
    choose: "Choose an app",
    remove: "Remove",
    unresolved: "Identity unresolved",
    noApps:
      "No identifiable apps are available. Open the app you want to exclude and try again.",
    shortcut: "Manual candidates",
    unassigned: "Unassigned",
    capture: "Set shortcut",
    captureHelp:
      "Press a key with Control, Alt, Command or Shift. Esc cancels.",
    clear: "Clear",
    cancel: "Cancel",
    done: "Done",
    currentVersion: "Current version",
    checkUpdate: "Check for updates",
    updateConsent: "Download and install",
    updateHelp:
      "Updates are checked at startup and every 24 hours. Downloads start only with your consent.",
    recovery:
      "If installation fails, restart FixKey or manually reinstall a signed package. Your settings are preserved. Previous signed versions can be reinstalled manually.",
    permission: "Allow accessibility access",
    permissionHelp:
      "FixKey needs accessibility and input observation access to identify an editable control and replace only the completed word.",
    inputPermission: "Allow Input Monitoring",
    inputPermissionRequest: "Allow Input Monitoring",
    inputPermissionSetup: "Set up Input Monitoring",
    inputPermissionListHelp:
      "Accessibility can already allow key listening even when FixKey is absent from Input Monitoring. macOS does not confirm a separate list entry here. Check the list in System Settings and add the current FixKey.app with + if it is missing.",
    inputPermissionHelp:
      "FixKey needs Input Monitoring to detect the keys used for keyboard-layout correction.",
    accessibilityGranted: "Accessibility access is already allowed.",
    inputPermissionSteps: [
      "Choose Allow Input Monitoring to register FixKey and open its macOS permission settings.",
      "In System Settings → Privacy & Security → Input Monitoring, enable FixKey. If it is missing, choose + and add the current FixKey.app from Applications. Return here and choose Recheck. Restart FixKey if macOS asks.",
    ],
    permissionRequest: "Allow accessibility access",
    permissionWaiting:
      "Correction will stay off until access is allowed. After enabling access, choose Recheck.",
    permissionSteps: [
      "Choose Allow accessibility access and follow the macOS prompt.",
      "Enable FixKey in Accessibility. If it is already enabled but this dialog remains after restarting FixKey, remove the old FixKey entry with − and use + to add the current FixKey.app again. A rebuilt app may no longer match the old permission entry. If it is missing, use + to add FixKey from Applications.",
      "Also enable FixKey in Input Monitoring, then recheck. If no input monitoring prompt appears, choose Allow accessibility access again. Restart FixKey if macOS asks.",
    ],
    macPermission:
      "In System Settings, allow FixKey under Accessibility and Input Monitoring, then recheck.",
    windowsPermission:
      "Use FixKey in your current Windows session. Protected, elevated or unsupported controls are left unchanged.",
    linuxPermission:
      "Use an X11 session with the AT-SPI accessibility service and XInput2 available. Wayland is unsupported.",
    openSettings: "Open system settings",
    recheck: "Recheck",
    later: "Later",
    error: "Action failed",
    reason: "Reason",
    openLogs: "Open log folder",
    deleteLogs: "Delete logs",
    restore: "Restore settings backup",
    support: "GitHub Issues",
    logHelp:
      "Logs contain version, platform, reason codes and timings. Input text is excluded. Logs stay on this device for at most seven days and 10 MiB total.",
    original: "Original",
    recognized: "Recognized",
    notRecognized: "Not recognized by the dictionary",
    unchecked: "Could not be checked",
    candidates: "Layout candidates",
    candidateHelp: "↑ ↓ Select · Enter Apply · Esc Close",
    noCandidates: "The source context is no longer available.",
    help: "Help",
    helpTopics: [
      [
        "Installation",
        "Install the signed DMG on macOS ARM64, NSIS installer on Windows x64, or AppImage on Linux X11 x64. Target platforms do not imply verified compatibility with every app.",
      ],
      [
        "Permissions",
        "Grant the accessibility and input permissions requested by your OS, then choose Recheck. Correction remains inactive until permissions and dictionaries are ready. On macOS, Accessibility can provide effective key-listening access without a separate Input Monitoring entry. Use Set up Input Monitoring in General to check the list; if FixKey is absent, use + to add the current FixKey.app from Applications.",
      ],
      [
        "Layouts and correction",
        "Korean Dubeolsik and US English QWERTY are enabled by default. A space or punctuation boundary triggers evaluation. Enter and Tab do not. Only one dictionary-recognized language permits automatic replacement. FixKey never switches your input language.",
      ],
      [
        "Undo",
        "Immediately press Backspace after automatic correction to restore the original word and its separator. Further Backspace presses behave normally.",
      ],
      [
        "Manual candidates",
        "Assign a shortcut, type a word and use the shortcut. Arrow keys select, Enter applies and Esc closes. You can explicitly choose an unrecognized candidate. Moving the source caret or changing apps closes the picker.",
      ],
      [
        "Exclusions",
        "Add an app under Excluded Apps. Exclusions match the app's stable identity. Unresolved identities remain listed.",
      ],
      [
        "Privacy",
        "FixKey processes input locally. It does not keep input history, use the clipboard for replacement, or send telemetry. Pasted, programmatic, numeric, emoji, URL, email and mixed-script tokens are excluded from automatic correction.",
      ],
      [
        "Updates",
        "Stable updates are checked over HTTPS at startup and every 24 hours. You consent before downloading. Invalid signatures are rejected. Correction stops before installation; settings remain intact.",
      ],
      [
        "Troubleshooting",
        "Read the reason code, recheck permissions, and try a supported editable control. Missing permissions, unknown field sensitivity, stale targets and unsupported writes cause safe refusal. Restart or manually reinstall a signed package after update failure.",
      ],
      [
        "Log deletion",
        "Choose Delete logs to remove local diagnostic logs, or Open log folder to inspect them. Do not include private content when opening a GitHub issue.",
      ],
    ],
  },
  ko: {
    general: "일반",
    exclusions: "제외 앱",
    shortcuts: "단축키",
    updates: "업데이트",
    diagnostics: "진단 및 도움말",
    tagline: "입력은 계속, 자판은 자동으로.",
    correction: "자동 교정",
    pause: "일시 정지",
    resume: "다시 시작",
    languages: "사용할 언어",
    korean: "한국어 · 두벌식",
    english: "영어 · US QWERTY",
    startup: "로그인 시 시작",
    uiLanguage: "화면 언어",
    theme: "테마",
    system: "시스템 설정",
    light: "밝게",
    dark: "어둡게",
    loading: "FixKey에 연결하고 있어요…",
    connection: "데스크톱 앱에 연결할 수 없어요.",
    retry: "다시 시도",
    active: "교정이 켜져 있어요",
    inactive: "교정이 꺼져 있어요",
    waitingField: "교정할 입력란을 기다리고 있어요",
    waitingFieldHelp:
      "다른 앱의 편집 가능한 입력란을 클릭해 주세요. 설정 화면을 보고 있거나 입력란을 안전하게 확인할 수 없을 때는 대기해요. 자세한 상태는 진단 및 도움말에서 확인할 수 있어요.",
    local: "입력 내용은 이 기기에만 머물러요. 비밀번호 입력란은 제외해요.",
    empty: "제외한 앱이 없어요",
    emptyDetail:
      "지원하는 입력란에서 교정할 수 있어요. 항상 원문을 유지할 앱을 추가해 주세요.",
    add: "앱 추가",
    choose: "앱 선택",
    remove: "제거",
    unresolved: "앱 식별 확인 필요",
    noApps: "식별할 수 있는 앱이 없어요. 제외할 앱을 연 뒤 다시 시도해 주세요.",
    shortcut: "수동 후보",
    unassigned: "지정하지 않음",
    capture: "단축키 지정",
    captureHelp:
      "Control, Alt, Command 또는 Shift와 함께 키를 눌러 주세요. Esc로 취소해요.",
    clear: "지우기",
    cancel: "취소",
    done: "완료",
    currentVersion: "현재 버전",
    checkUpdate: "업데이트 확인",
    updateConsent: "다운로드 및 설치",
    updateHelp:
      "시작할 때와 24시간마다 업데이트를 확인해요. 동의한 뒤에만 다운로드해요.",
    recovery:
      "설치에 실패하면 FixKey를 다시 시작하거나 서명된 패키지를 직접 다시 설치해 주세요. 설정은 유지돼요. 이전 서명 버전도 직접 다시 설치할 수 있어요.",
    permission: "접근성 권한을 허용해 주세요",
    permissionHelp:
      "FixKey는 입력란을 확인하고 입력한 단어만 바꾸기 위해 접근성 및 입력 관찰 권한이 필요해요.",
    inputPermission: "입력 모니터링을 허용해 주세요",
    inputPermissionRequest: "입력 모니터링 허용하기",
    inputPermissionSetup: "입력 모니터링 설정",
    inputPermissionListHelp:
      "입력 모니터링 목록에 FixKey가 없어도 접근성 권한으로 키 감지가 허용될 수 있어요. 앱에서는 별도 목록 등록 여부를 확인할 수 없어요. 시스템 설정에서 목록을 확인하고, 없다면 + 버튼으로 현재 FixKey.app을 추가해 주세요.",
    inputPermissionHelp:
      "잘못된 자판으로 입력한 단어를 교정하려면 누른 키를 확인할 수 있는 입력 모니터링 권한이 필요해요.",
    accessibilityGranted: "접근성 권한은 이미 허용되어 있어요.",
    inputPermissionSteps: [
      "‘입력 모니터링 허용하기’를 누르면 FixKey를 권한 목록에 등록하고 macOS 권한 설정을 열어요.",
      "시스템 설정 → 개인정보 보호 및 보안 → 입력 모니터링에서 FixKey를 켜 주세요. 목록에 없다면 + 버튼으로 응용 프로그램의 현재 FixKey.app을 추가해 주세요. 돌아와서 ‘다시 확인’을 눌러 주세요. macOS가 요청하면 FixKey를 다시 시작해 주세요.",
    ],
    permissionRequest: "접근성 허용하기",
    permissionWaiting:
      "권한을 허용하기 전까지 자동 교정은 꺼져 있어요. 허용한 뒤 ‘다시 확인’을 눌러 주세요.",
    permissionSteps: [
      "‘접근성 허용하기’를 누르고 macOS 안내를 따라 주세요.",
      "손쉬운 사용에서 FixKey를 켜 주세요. FixKey를 다시 시작해도 이 안내가 계속 나오면 − 버튼으로 기존 FixKey 항목을 삭제하고 + 버튼으로 현재 FixKey.app을 다시 추가해 주세요. 앱을 새로 빌드하면 이전 권한 등록과 서명이 달라질 수 있어요. 목록에 없다면 + 버튼으로 응용 프로그램의 FixKey를 추가해 주세요.",
      "입력 모니터링에서도 FixKey를 켠 뒤 다시 확인해 주세요. 요청 창이 나타나지 않으면 ‘접근성 허용하기’를 다시 눌러 주세요. macOS가 요청하면 FixKey를 다시 시작해 주세요.",
    ],
    macPermission:
      "시스템 설정의 손쉬운 사용과 입력 모니터링에서 FixKey를 허용한 뒤 다시 확인해 주세요.",
    windowsPermission:
      "현재 Windows 세션에서 사용해 주세요. 보호된 입력란, 관리자 권한 앱 또는 지원하지 않는 입력란은 바꾸지 않아요.",
    linuxPermission:
      "AT-SPI 접근성 서비스와 XInput2를 사용할 수 있는 X11 세션이 필요해요. Wayland는 지원하지 않아요.",
    openSettings: "시스템 설정 열기",
    recheck: "다시 확인",
    later: "나중에",
    error: "작업을 완료하지 못했어요",
    reason: "원인",
    openLogs: "로그 폴더 열기",
    deleteLogs: "로그 삭제",
    restore: "설정 백업 복원",
    support: "GitHub Issues",
    logHelp:
      "로그에는 버전, 플랫폼, 원인 코드와 처리 시간만 남겨요. 입력 내용은 제외해요. 이 기기에 최대 7일, 전체 10 MiB까지 보관해요.",
    original: "원문",
    recognized: "사전에서 확인됨",
    notRecognized: "사전에 없는 후보",
    unchecked: "검증할 수 없음",
    candidates: "자판 후보",
    candidateHelp: "↑ ↓ 선택 · Enter 적용 · Esc 닫기",
    noCandidates: "원래 입력 위치를 더 이상 확인할 수 없어요.",
    help: "도움말",
    helpTopics: [
      [
        "설치",
        "macOS ARM64는 서명된 DMG, Windows x64는 NSIS 설치 파일, Linux X11 x64는 AppImage를 사용해 주세요. 대상 플랫폼이 모든 앱과의 호환성을 보장하지는 않아요.",
      ],
      [
        "권한",
        "운영체제에서 요청하는 접근성 및 입력 권한을 허용한 뒤 다시 확인해 주세요. 권한과 사전이 준비될 때까지 교정은 꺼져 있어요. macOS에서는 별도 입력 모니터링 목록 등록 없이도 접근성 권한으로 키 감지가 허용될 수 있어요. 일반의 입력 모니터링 설정에서 목록을 확인하고, 없다면 + 버튼으로 응용 프로그램의 현재 FixKey.app을 추가해 주세요.",
      ],
      [
        "자판과 교정",
        "한국어 두벌식과 영어 US QWERTY가 기본으로 켜져 있어요. 공백과 문장부호에서 단어를 확인하며 Enter와 Tab은 교정을 시작하지 않아요. 하나의 언어에서만 단어를 인정할 때 자동으로 바꿔요. 입력 언어는 바꾸지 않아요.",
      ],
      [
        "되돌리기",
        "자동 교정 직후 Backspace를 누르면 원문과 구분 문자를 복원해요. 다음 Backspace부터는 평소처럼 삭제해요.",
      ],
      [
        "수동 후보",
        "단축키를 지정하고 단어를 입력한 뒤 단축키를 눌러 주세요. 화살표로 선택하고 Enter로 적용하며 Esc로 닫아요. 사전에 없는 후보도 직접 선택할 수 있어요. 커서나 앱이 바뀌면 후보를 닫아요.",
      ],
      [
        "제외 앱",
        "제외 앱에서 앱을 추가해 주세요. 앱의 고유 식별자로 일치 여부를 판단해요. 식별할 수 없는 항목도 목록에 유지해요.",
      ],
      [
        "개인정보",
        "기기 안에서만 처리하며 입력 기록, 클립보드 대체 입력, 원격 사용량 수집을 사용하지 않아요. 붙여넣기, 프로그램 입력, 숫자, 이모지, URL, 이메일과 혼합 문자 토큰은 자동 교정에서 제외해요.",
      ],
      [
        "업데이트",
        "시작할 때와 24시간마다 HTTPS로 안정 버전을 확인해요. 동의한 뒤 다운로드하고 서명이 잘못되면 거부해요. 설치 전에 교정을 멈추고 설정은 보존해요.",
      ],
      [
        "문제 해결",
        "원인 코드를 확인하고 권한을 다시 확인하거나 지원하는 입력란을 사용해 주세요. 권한, 입력란의 민감도, 대상 위치 또는 쓰기 지원을 확인할 수 없으면 교정하지 않아요. 업데이트 실패 시 다시 시작하거나 서명된 패키지를 직접 재설치해 주세요.",
      ],
      [
        "로그 삭제",
        "로그 삭제로 진단 로그를 지우거나 로그 폴더 열기로 확인할 수 있어요. GitHub 이슈에는 개인적인 입력 내용을 포함하지 마세요.",
      ],
    ],
  },
} as const;
