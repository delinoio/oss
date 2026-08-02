import { readdir, readFile } from "node:fs/promises";
import { extname, join, relative, resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const distRoot = resolve(appRoot, "dist");
const failures = [];

function requireCondition(condition, message) {
  if (!condition) failures.push(message);
}

async function read(relativePath) {
  return readFile(resolve(appRoot, relativePath), "utf8");
}

async function filesUnder(directory, excludedDirectories = new Set()) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (excludedDirectories.has(entry.name)) continue;
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await filesUnder(path, excludedDirectories)));
    } else if (entry.isFile()) {
      files.push(path);
    }
  }
  return files.toSorted();
}

const [
  androidManifest,
  authCoreSource,
  authNativeSource,
  cargoManifest,
  desktopCapabilitySource,
  diagnosticsSource,
  extensionBuildSource,
  extensionManifestSource,
  extensionProtocolSource,
  extensionServiceWorkerSource,
  iosEntitlements,
  iosInfo,
  iosProject,
  mobileCapabilitySource,
  packageSource,
  realqaCaptureCapabilitySource,
  realqaComposerCapabilitySource,
  realqaNativeHostSource,
  rustBuildSource,
  rustSource,
  settingsCapabilitySource,
  tauriConfigSource,
  updaterSource,
] = await Promise.all([
  read("src-tauri/gen/android/app/src/main/AndroidManifest.xml"),
  read("src-tauri/src/auth.rs"),
  read("src-tauri/src/auth_native.rs"),
  read("src-tauri/Cargo.toml"),
  read("src-tauri/capabilities/desktop-main.json"),
  read("src-tauri/src/diagnostics.rs"),
  read("scripts/build-realqa-extension.mjs"),
  read("realqa-extension/manifest.template.json"),
  read("realqa-extension/src/protocol.js"),
  read("realqa-extension/src/service-worker.js"),
  read("src-tauri/gen/apple/devhud_iOS/devhud_iOS.entitlements"),
  read("src-tauri/gen/apple/devhud_iOS/Info.plist"),
  read("src-tauri/gen/apple/project.yml"),
  read("src-tauri/capabilities/mobile-main.json"),
  read("package.json"),
  read("src-tauri/capabilities/realqa-capture.json"),
  read("src-tauri/capabilities/realqa-composer.json"),
  read("src-tauri/src/realqa_native_host.rs"),
  read("src-tauri/build.rs"),
  read("src-tauri/src/lib.rs"),
  read("src-tauri/capabilities/settings.json"),
  read("src-tauri/tauri.conf.json"),
  read("src-tauri/src/updater.rs"),
]);

const packageJson = JSON.parse(packageSource);
const extensionManifest = JSON.parse(extensionManifestSource);
const tauriConfig = JSON.parse(tauriConfigSource);
const capabilities = {
  "desktop-main": JSON.parse(desktopCapabilitySource),
  settings: JSON.parse(settingsCapabilitySource),
  "mobile-main": JSON.parse(mobileCapabilitySource),
  "realqa-capture": JSON.parse(realqaCaptureCapabilitySource),
  "realqa-composer": JSON.parse(realqaComposerCapabilitySource),
};

requireCondition(
  extensionManifest.manifest_version === 3 &&
    extensionManifest.minimum_chrome_version === "150" &&
    extensionManifest.incognito === "not_allowed",
  "RealQA extension must target Chrome 150 MV3 and exclude Incognito",
);
requireCondition(
  JSON.stringify(extensionManifest.permissions) ===
    JSON.stringify([
      "activeTab",
      "tabs",
      "scripting",
      "nativeMessaging",
      "storage",
    ]) &&
    JSON.stringify(extensionManifest.optional_host_permissions) ===
      JSON.stringify(["https://*/*", "http://*/*"]) &&
    extensionManifest.host_permissions === undefined &&
    extensionManifest.content_scripts === undefined,
  "RealQA extension must keep exact active-tab/native permissions and optional-only DOM origins",
);
requireCondition(
  !extensionManifestSource.includes("<all_urls>") &&
    !extensionManifestSource.includes("desktopCapture") &&
    !extensionManifestSource.includes("update_url"),
  "RealQA extension must exclude all-URL, desktop/full-page, and store publication authority",
);
requireCondition(
  extensionServiceWorkerSource.includes("chrome.tabs.captureVisibleTab") &&
    extensionServiceWorkerSource.includes("captureMode: \"os-capture\"") &&
    extensionServiceWorkerSource.includes("chrome.permissions.contains") &&
    extensionServiceWorkerSource.includes("chrome.permissions.remove") &&
    extensionServiceWorkerSource.includes("capturedTabId") &&
    extensionServiceWorkerSource.includes("capturedWindowId") &&
    extensionServiceWorkerSource.includes("tab.url !== capturedUrl") &&
    extensionServiceWorkerSource.includes("chrome.storage.session") &&
    !extensionServiceWorkerSource.includes("captureBeyondViewport") &&
    !extensionServiceWorkerSource.includes("debugger"),
  "RealQA extension must use visible-viewport capture, immutable tab identity, temporary exact-origin permission, and restricted-page OS fallback",
);
requireCondition(
  extensionProtocolSource.includes("25 * 1024 * 1024") &&
    extensionProtocolSource.includes("64 * 1024 * 1024") &&
    extensionProtocolSource.includes("1024 * 1024") &&
    extensionProtocolSource.includes("sanitizeSelection") &&
    !extensionProtocolSource.includes("innerHTML"),
  "RealQA extension protocol must retain size bounds and the closed sanitized metadata shape",
);
requireCondition(
  realqaNativeHostSource.includes("deny_unknown_fields") &&
    realqaNativeHostSource.includes("validate_origin") &&
    realqaNativeHostSource.includes("SocketComposerDelivery") &&
    realqaNativeHostSource.includes("start_composer_listener") &&
    realqaNativeHostSource.includes('.arg("--realqa-composer")') &&
    !realqaNativeHostSource.includes("INBOX_DIRECTORY") &&
    rustSource.includes("has_prior_feature_binding") &&
    rustSource.includes("RealQaBrowserInbox") &&
    rustSource.includes('argument == "--realqa-composer"') &&
    rustSource.includes("build_realqa_composer_window") &&
    realqaNativeHostSource.includes("MAX_ENCODED_IMAGE_BYTES") &&
    realqaNativeHostSource.includes("MAX_EXTENSION_MESSAGE_BYTES") &&
    realqaNativeHostSource.includes("MAX_HOST_RESPONSE_BYTES"),
  "RealQA native host must revalidate schema/origin, use authenticated memory-only composer IPC, and retain framing limits",
);
requireCondition(
  extensionBuildSource.includes("FIXTURE_EXTENSION_ID") &&
    extensionBuildSource.includes("DEVHUD_CHROME_EXTENSION_ID") &&
    extensionBuildSource.includes("DEVHUD_APPROVED_CHROME_EXTENSION_IDS") &&
    !Object.keys(packageJson.scripts).some((script) =>
      /publish.*(?:chrome|extension)|(?:chrome|extension).*publish/iu.test(script),
    ),
  "RealQA packaging must use a fixture ID, require external production approval, and expose no store publication task",
);
const expectedCapabilities = {
  "desktop-main": {
    platforms: ["linux", "macOS", "windows"],
    windows: ["main"],
    permissions: [
      "allow-get-runtime-info",
      "allow-read-settings",
      "allow-read-widget-configuration",
      "allow-hide-hud",
      "allow-show-settings",
      "allow-show-realqa",
      "allow-get-auth-session",
      "allow-start-authentication",
      "allow-logout-authentication",
    ],
  },
  settings: {
    platforms: ["linux", "macOS", "windows"],
    windows: ["settings"],
    permissions: [
      "allow-get-runtime-info",
      "allow-read-settings",
      "allow-write-settings",
      "allow-read-shortcut-effective-state",
      "allow-write-shortcut-effective-state",
      "allow-read-widget-configuration",
      "allow-export-diagnostics",
      "allow-reset-dev-hud",
      "allow-hide-settings",
      "allow-replace-global-shortcut",
      "allow-set-launch-at-login",
      "allow-complete-first-run",
      "allow-request-update-action",
      "allow-get-auth-session",
      "allow-start-authentication",
      "allow-logout-authentication",
    ],
  },
  "mobile-main": {
    platforms: ["iOS", "android"],
    windows: ["main"],
    permissions: [
      "allow-get-runtime-info",
      "allow-read-settings",
      "allow-write-settings",
      "allow-read-shortcut-effective-state",
      "allow-write-shortcut-effective-state",
      "allow-read-widget-configuration",
      "allow-write-widget-configuration",
      "allow-export-diagnostics",
      "allow-reset-dev-hud",
      "allow-get-auth-session",
      "allow-start-authentication",
      "allow-logout-authentication",
    ],
  },
  "realqa-capture": {
    platforms: ["linux", "macOS", "windows"],
    windows: ["realqa-capture"],
    permissions: [
      "allow-realqa-capture-permission-status",
      "allow-realqa-request-capture-permission",
      "allow-realqa-inspect-capture-capabilities",
      "allow-realqa-list-capture-sources",
      "allow-realqa-adjust-capture-selection",
      "allow-realqa-begin-capture",
      "allow-realqa-cancel-capture",
    ],
  },
  "realqa-composer": {
    platforms: ["linux", "macOS", "windows"],
    windows: ["realqa-composer"],
    permissions: [
      "allow-get-runtime-info",
      "allow-realqa-composer-accept-image",
      "allow-realqa-composer-flatten-image",
      "allow-realqa-composer-remove-image",
      "allow-realqa-composer-reset-session",
      "allow-realqa-begin-browser-fallback-capture",
      "allow-realqa-take-browser-capture",
      "allow-realqa-get-local-draft-status",
      "allow-realqa-list-local-drafts",
      "allow-realqa-save-local-draft",
      "allow-realqa-load-local-draft",
      "allow-realqa-delete-local-draft",
      "allow-realqa-assert-local-draft-submission-allowed",
      "allow-realqa-connect",
      "allow-realqa-signed-put",
      "allow-get-auth-session",
      "allow-start-authentication",
      "allow-logout-authentication",
    ],
  },
};

requireCondition(
  JSON.stringify(tauriConfig.app?.security?.capabilities) ===
    JSON.stringify(Object.keys(expectedCapabilities)),
  "Tauri must enable only the five window- and platform-specific capabilities",
);
for (const [identifier, expected] of Object.entries(expectedCapabilities)) {
  const actual = capabilities[identifier];
  requireCondition(actual?.identifier === identifier, `${identifier} must retain its identifier`);
  requireCondition(actual?.local === true, `${identifier} must reject remote origins`);
  requireCondition(
    actual?.remote === undefined,
    `${identifier} must not declare a remote capability`,
  );
  for (const field of ["platforms", "windows", "permissions"]) {
    requireCondition(
      JSON.stringify(actual?.[field]) === JSON.stringify(expected[field]),
      `${identifier} must preserve its exact ${field} scope`,
    );
  }
}

const prohibitedPermission = /(?:^|:)(?:default|fs|http|opener|os|process|screen|shell|store)(?::|$)|(?:dialog|download|upload)/u;
for (const [identifier, capability] of Object.entries(capabilities)) {
  requireCondition(
    capability.permissions.every(
      (permission) =>
        permission.startsWith("allow-") && !prohibitedPermission.test(permission),
    ),
    `${identifier} must contain explicit app-command grants and no broad plugin defaults`,
  );
}

const commandFromPermission = (permission) => permission.replace(/^allow-/u, "").replaceAll("-", "_");
const maliciousCommands = [
  "plugin:fs|read_file",
  "plugin:http|fetch",
  "plugin:opener|open_url",
  "plugin:os|hostname",
  "plugin:os|locale",
  "plugin:process|relaunch",
  "plugin:screen|capture",
  "plugin:shell|execute",
  "plugin:store|get",
  "undeclared_command",
];
for (const [surface, capabilityId] of [
  ["desktop-hud-normal-view", "desktop-main"],
  ["desktop-hud-devtools", "desktop-main"],
  ["desktop-settings-normal-view", "settings"],
  ["desktop-settings-devtools", "settings"],
  ["mobile-normal-view", "mobile-main"],
  ["realqa-capture-normal-view", "realqa-capture"],
  ["realqa-capture-devtools", "realqa-capture"],
  ["realqa-composer-normal-view", "realqa-composer"],
  ["realqa-composer-devtools", "realqa-composer"],
]) {
  const commands = new Set(
    capabilities[capabilityId].permissions.map(commandFromPermission),
  );
  for (const command of maliciousCommands) {
    requireCondition(!commands.has(command), `${surface} granted malicious IPC ${command}`);
  }
}

const captureCommands = new Set(
  capabilities["realqa-capture"].permissions.map(commandFromPermission),
);
const composerCommands = new Set(
  capabilities["realqa-composer"].permissions.map(commandFromPermission),
);
for (const capabilityId of ["desktop-main", "settings", "mobile-main"]) {
  const commands = new Set(
    capabilities[capabilityId].permissions.map(commandFromPermission),
  );
  requireCondition(
    [...commands].every((command) => !command.startsWith("realqa_")),
    `${capabilityId} must deny every RealQA command`,
  );
}
requireCondition(
  [...captureCommands].every((command) => !command.startsWith("realqa_composer_")),
  "the capture window must deny composer image/session commands",
);
requireCondition(
  [...composerCommands].every(
    (command) =>
      (command.startsWith("realqa_composer_") ||
        command === "get_runtime_info" ||
        command === "realqa_begin_browser_fallback_capture" ||
        command === "realqa_take_browser_capture" ||
        command.startsWith("realqa_get_local_draft_") ||
        command.startsWith("realqa_list_local_drafts") ||
        command.startsWith("realqa_save_local_draft") ||
        command.startsWith("realqa_load_local_draft") ||
        command.startsWith("realqa_delete_local_draft") ||
        command.startsWith("realqa_assert_local_draft_") ||
        command === "realqa_connect" ||
        command === "realqa_signed_put" ||
        command === "get_auth_session" ||
        command === "start_authentication" ||
        command === "logout_authentication") &&
      ![
        "realqa_capture_permission_status",
        "realqa_request_capture_permission",
        "realqa_inspect_capture_capabilities",
        "realqa_list_capture_sources",
        "realqa_adjust_capture_selection",
        "realqa_begin_capture",
        "realqa_cancel_capture",
      ].includes(command),
  ),
  "the composer window must deny capture source and pixel commands",
);

const csp = tauriConfig.app?.security?.csp ?? "";
for (const directive of [
  "default-src 'self'",
  "base-uri 'none'",
  "connect-src ipc: http://ipc.localhost",
  "font-src 'none'",
  "form-action 'none'",
  "frame-ancestors 'none'",
  "frame-src 'none'",
  "manifest-src 'none'",
  "media-src 'none'",
  "object-src 'none'",
  "script-src 'self'",
  "style-src 'self'",
  "worker-src 'none'",
]) {
  requireCondition(csp.includes(directive), `CSP must contain ${directive}`);
}
requireCondition(
  tauriConfig.build?.frontendDist === "../dist" &&
    tauriConfig.build?.devUrl === undefined &&
    tauriConfig.app?.windows?.length === 0 &&
    tauriConfig.plugins === undefined,
  "the shell must load only bundled assets and create guarded windows without plugins",
);

requireCondition(
  /tauri-runtime-cef\s*=\s*\{[^}]*rev\s*=\s*"f49ebda2fdba5755456b0f049e32593ca0ea331a"[^}]*default-features\s*=\s*false[^}]*features\s*=\s*\["devtools", "sandbox"\]/u.test(
    cargoManifest,
  ),
  "desktop must retain the exact upstream CEF pin with sandbox and preview DevTools",
);
requireCondition(
  packageJson.devDependencies?.["@tauri-apps/cli-cef"] === "3.0.0-alpha.6" &&
    packageJson.devDependencies?.["@tauri-apps/cli-mobile"] ===
      "npm:@tauri-apps/cli@2.11.4",
  "desktop CEF and standard mobile CLI pins must remain exact",
);
requireCondition(
  packageJson.scripts?.["build:preview"]?.includes(
    "--features desktop-cef,custom-protocol",
  ) &&
    packageJson.scripts?.["build:desktop"]?.includes(
      "--features desktop-cef,custom-protocol",
    ),
  "desktop debug and signed-preview commands must compile the sandboxed CEF feature",
);
requireCondition(
  cargoManifest.includes(
    'mobile-system-webview = ["dep:tauri", "dep:tauri-runtime-wry"]',
  ) &&
    !cargoManifest
      .match(
        /\[target\.'cfg\(any\(target_os = "android", target_os = "ios"\)\)'\.dependencies\]([\s\S]*?)(?=\n\[)/u,
      )?.[1]
      .includes("cef"),
  "mobile must remain on standard Tauri system webviews without CEF",
);

for (const guard of [
  ".on_navigation(is_bundled_url)",
  ".on_new_window(|_, _| NewWindowResponse::Deny)",
  ".on_download(|_, _| false)",
  ".on_web_resource_request(apply_web_resource_policy)",
]) {
  requireCondition(
    (rustSource.match(new RegExp(guard.replaceAll(/[.*+?^${}()|[\]\\]/gu, "\\$&"), "gu")) ?? [])
      .length === 4,
    `all four desktop and mobile webviews must apply ${guard}`,
  );
}
for (const denial of [
  "StatusCode::FORBIDDEN",
  "WebResourceDecision::Deny",
  '"--disable-background-networking"',
  '"--disable-component-update"',
  '"--disable-domain-reliability"',
  '"host-resolver-rules"',
  '"MAP * ~NOTFOUND, EXCLUDE tauri.localhost"',
  ".root_cache_path(profile)",
]) {
  requireCondition(rustSource.includes(denial), `desktop resource policy must contain ${denial}`);
}
requireCondition(
  (rustSource.match(/\.devtools\(true\)/gu) ?? []).length === 3 &&
    rustSource.includes(".devtools(false)"),
  "DevTools must be limited to the guarded desktop development/preview windows",
);
requireCondition(
  rustSource.includes(
    "normal_views_and_devtools_deny_malicious_navigation_and_remote_resources",
  ),
  "Rust tests must exercise malicious navigation and resources through normal-view and DevTools-equivalent policies",
);
requireCondition(
  rustSource.includes("malicious_remote_resource_responses_are_replaced_with_empty_denials"),
  "Rust tests must verify denied remote responses cannot retain attacker-controlled data",
);
requireCondition(
  rustSource.includes("local_ipc_responses_keep_internal_transport_headers_and_body"),
  "Rust tests must verify resource hardening preserves only the local IPC transport",
);
for (const surface of [
  "desktop-hud-normal-view",
  "desktop-hud-devtools",
  "desktop-settings-normal-view",
  "desktop-settings-devtools",
]) {
  requireCondition(
    rustSource.includes(".on_navigation(is_bundled_url)") &&
      rustSource.includes(".on_new_window(|_, _| NewWindowResponse::Deny)") &&
      rustSource.includes(".on_download(|_, _| false)") &&
      rustSource.includes(".on_web_resource_request(apply_web_resource_policy)"),
    `${surface} must inherit navigation, popup, download, and resource denial`,
  );
}

for (const dependency of [
  "ureq",
  "hyper",
  "tauri-plugin-fs",
  "tauri-plugin-http",
  "tauri-plugin-opener",
  "tauri-plugin-process",
  "tauri-plugin-shell",
  "tauri-plugin-store",
]) {
  requireCondition(
    !cargoManifest.includes(dependency),
    `the application manifest must not grant process, filesystem, or network authority through ${dependency}`,
  );
}
requireCondition(
  !/(reqwest|ureq|github\.com|https?:\/\/)/u.test(updaterSource) &&
    updaterSource.includes("ScopedUpdaterUnavailable") &&
    updaterSource.includes("RateLimited") &&
    updaterSource.includes("InvalidSignature") &&
    updaterSource.includes("InstallationFailed"),
  "the updater must remain networkless while preserving closed future failure enums",
);
for (const boundary of [
  'endpoint("/oidc/auth")',
  'endpoint("/oidc/token")',
  'endpoint("/oidc/token/revocation")',
  'endpoint.set_path("/oidc/jwks")',
  '"https://deli.dev/auth/devhud/callback"',
  'const DESKTOP_CALLBACK_PATH: &str = "/auth/callback"',
  '"http://127.0.0.1:{port}{DESKTOP_CALLBACK_PATH}"',
  "CallbackAlreadyConsumed",
  "AccountSwitchRequiresLogout",
]) {
  requireCondition(
    authCoreSource.includes(boundary),
    `the native auth core must retain ${boundary}`,
  );
}
for (const boundary of [
  ".https_only(true)",
  ".redirect(Policy::none())",
  '("resource", audience)',
  "validation.set_issuer",
  "validation.set_audience",
  "VAULT_ACCOUNT",
]) {
  requireCondition(
    authNativeSource.includes(boundary),
    `the native authentication adapter must retain ${boundary}`,
  );
}
requireCondition(
  !/telemetry|analytics|sentry|datadog|segment|newrelic/iu.test(
    `${cargoManifest}\n${packageSource}`,
  ),
  "DevHud must not add a client telemetry dependency",
);

const declaredCommands = new Set(
  [...rustBuildSource.matchAll(/^\s*"([a-z][a-z0-9_]*)",$/gmu)].map(
    (match) => match[1],
  ),
);
const frontendFiles = await filesUnder(resolve(appRoot, "src"));
const frontendText = await Promise.all(
  frontendFiles
    .filter((path) => [".ts", ".tsx"].includes(extname(path)))
    .map((path) => readFile(path, "utf8")),
);
const authFrontendText = (
  await Promise.all(
    frontendFiles
      .filter((path) => path.includes("/src/auth/"))
      .filter((path) => !path.includes(".test."))
      .filter((path) => [".ts", ".tsx"].includes(extname(path)))
      .map((path) => readFile(path, "utf8")),
  )
).join("\n");
requireCondition(
  !/\b(?:localStorage|sessionStorage|indexedDB|caches\.|serviceWorker|persistQueryClient|createSyncStoragePersister)\b/u.test(
    authFrontendText,
  ),
  "authentication must not use browser, service-worker, or React Query persistence",
);
requireCondition(
  !/\b(?:fetch|XMLHttpRequest|WebSocket|EventSource)\b/u.test(authFrontendText) &&
    !authFrontendText.includes("getAccessToken") &&
    !authFrontendText.includes("getIdToken"),
  "frontend authentication must have no network or token getter",
);
const invokedCommands = new Set(
  frontendText.flatMap((source) =>
    [
      ...source.matchAll(
        /\b(?:invoke|invokeCommand)(?:<[^;]*?>)?\(\s*"([a-z][a-z0-9_]*)"/gu,
      ),
    ].map((match) => match[1]),
  ),
);
for (const command of invokedCommands) {
  requireCondition(declaredCommands.has(command), `frontend invokes undeclared command ${command}`);
  requireCondition(
    Object.values(capabilities).some((capability) =>
      capability.permissions.includes(
        `allow-${command.replaceAll("_", "-")}`,
      ),
    ),
    `frontend command ${command} has no explicit capability`,
  );
}

requireCondition(
  (androidManifest.match(/android\.permission\.INTERNET/gu) ?? []).length === 1 &&
    (androidManifest.match(/android\.intent\.category\.BROWSABLE/gu) ?? []).length === 1 &&
    androidManifest.includes('android:scheme="https"') &&
    androidManifest.includes('android:host="deli.dev"') &&
    androidManifest.includes('android:path="/auth/devhud/callback"') &&
    androidManifest.includes('android:usesCleartextTraffic="false"') &&
    androidManifest.includes('android:allowBackup="false"'),
  "Android must limit network/deep-link authority to native auth and deny cleartext/backup",
);
requireCondition(
  !iosInfo.includes("CFBundleURLTypes") &&
    !iosInfo.includes("CFBundleURLSchemes") &&
    iosEntitlements.includes("com.apple.developer.associated-domains") &&
    (iosEntitlements.match(/applinks:/gu) ?? []).length === 1 &&
    iosEntitlements.includes("applinks:deli.dev") &&
    !iosProject.includes("WidgetKit") &&
    !iosProject.includes(".appex"),
  "iOS must contain only the verified DeliDev associated-domain and no custom scheme/widget",
);

requireCondition(
  diagnosticsSource.includes("export_recursively_rejects_unknown_and_adversarial_values") &&
    diagnosticsSource.includes("#[serde(rename_all = \"camelCase\", deny_unknown_fields)]") &&
    diagnosticsSource.includes("record.is_valid().then_some(record)"),
  "diagnostics must recursively reject adversarial or unknown records",
);

const bundleFiles = await filesUnder(distRoot);
requireCondition(bundleFiles.length > 1, "generated frontend bundle must contain assets");
requireCondition(
  bundleFiles.every((path) => path.startsWith(`${distRoot}/`)),
  "bundle inspection must stay within dist",
);
const indexPath = resolve(distRoot, "index.html");
const indexSource = await readFile(indexPath, "utf8");
for (const match of indexSource.matchAll(/\b(?:href|src)="([^"]+)"/gu)) {
  const asset = match[1];
  requireCondition(
    !/^(?:[a-z][a-z0-9+.-]*:|\/\/)/iu.test(asset),
    `generated index references non-bundled asset ${asset}`,
  );
}
const textBundleExtensions = new Set([".css", ".html", ".js", ".json", ".map", ".svg", ".txt"]);
for (const path of bundleFiles.filter((file) =>
  textBundleExtensions.has(extname(file)),
)) {
  const source = await readFile(path, "utf8");
  const bundlePath = relative(distRoot, path).replaceAll("\\", "/");
  for (const match of source.matchAll(/https?:\/\/[^\s"'`<>)\\]+/giu)) {
    const endpoint = match[0].replace(/[;,]+$/u, "");
    const inertReactLiteral =
      bundlePath.startsWith("static/js/lib-react.") &&
      (endpoint.startsWith("https://react.dev/errors/") ||
        endpoint.startsWith("http://www.w3.org/"));
    const inertProtobufParserLiteral = endpoint === "http://${e}/";
    requireCondition(
      endpoint === "http://ipc.localhost" ||
        endpoint.startsWith("http://tauri.localhost") ||
        inertProtobufParserLiteral ||
        inertReactLiteral,
      `generated bundle contains remote endpoint ${endpoint} in ${bundlePath}`,
    );
  }
}

const repositoryFiles = await filesUnder(appRoot, new Set([
  ".gradle",
  "DerivedData",
  "Externals",
  "build",
  "dist",
  "node_modules",
  "target",
]));
const nativeTextExtensions = new Set([
  ".java",
  ".kt",
  ".m",
  ".mm",
  ".plist",
  ".swift",
  ".xml",
  ".yml",
]);
for (const path of repositoryFiles.filter((file) =>
  nativeTextExtensions.has(extname(file)),
)) {
  const source = await readFile(path, "utf8");
  requireCondition(
    !/https?:\/\/(?!(?:schemas\.android\.com|www\.apple\.com\/DTDs\/))/u.test(source),
    `native surface contains an unintended endpoint: ${relative(appRoot, path)}`,
  );
}

if (failures.length > 0) {
  throw new Error(`DevHud security checks failed:\n- ${failures.join("\n- ")}`);
}

console.log(
  JSON.stringify({
    bundledFiles: bundleFiles.map((path) =>
      relative(distRoot, path).replaceAll("\\", "/"),
    ),
    capabilities: Object.keys(expectedCapabilities),
    check: "devhud-deny-by-default-security",
    cefSandbox: true,
    devtoolsAuthorityExpansion: false,
    remoteFrontendResources: false,
    status: "passed",
  }),
);
