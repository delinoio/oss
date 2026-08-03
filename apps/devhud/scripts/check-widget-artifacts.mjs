import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { access, readFile, readdir } from "node:fs/promises";
import { delimiter, extname, resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const failures = [];
let androidArtifactsInspected = 0;
let iosArtifactsInspected = 0;

function requireCondition(condition, message) {
  if (!condition) failures.push(message);
}

async function read(relativePath) {
  return readFile(resolve(appRoot, relativePath), "utf8");
}

async function collectFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      if ([".gradle", "DerivedData", ".build"].includes(entry.name)) continue;
      files.push(...(await collectFiles(path)));
    } else {
      files.push(path);
    }
  }
  return files;
}

const [
  androidAppBuild,
  androidAppManifest,
  androidFoundationBuild,
  androidFoundationManifest,
  androidPluginManifest,
  androidPluginSource,
  androidNotificationSource,
  androidConfigurationActivitySource,
  androidProviderSource,
  androidProviderInfo,
  fixtureSource,
  iosAppEntitlements,
  iosAppProject,
  iosExtensionSource,
  iosPluginSource,
  iosNotificationSource,
  iosWidgetEntitlements,
  iosWidgetProject,
  packageSource,
  privatePluginManifest,
  privatePluginLibrarySource,
  privatePluginRustSource,
  productionRegistry,
  swiftConfigurationSource,
  kotlinConfigurationSource,
] = await Promise.all([
  read("src-tauri/gen/android/app/build.gradle.kts"),
  read("src-tauri/gen/android/app/src/main/AndroidManifest.xml"),
  read("native-widgets/android/widget-foundation/build.gradle.kts"),
  read("native-widgets/android/widget-foundation/src/main/AndroidManifest.xml"),
  read("src-tauri/widget-bridge/android/src/main/AndroidManifest.xml"),
  read(
    "src-tauri/widget-bridge/android/src/main/java/dev/deli/devhud/widget/DevHudWidgetPlugin.kt",
  ),
  read(
    "native-widgets/android/widget-foundation/src/main/java/dev/deli/devhud/widget/DeckNotificationPublisher.kt",
  ),
  read(
    "native-widgets/android/widget-foundation/src/main/java/dev/deli/devhud/widget/DevHudWidgetConfigurationActivity.kt",
  ),
  read(
    "native-widgets/android/widget-foundation/src/main/java/dev/deli/devhud/widget/DevHudWidgetProvider.kt",
  ),
  read("native-widgets/android/widget-foundation/src/main/res/xml/devhud_widget_info.xml"),
  read("native-widgets/fixtures/widget-configuration.v1.json"),
  read("src-tauri/gen/apple/devhud_iOS/devhud_iOS.entitlements"),
  read("src-tauri/gen/apple/project.yml"),
  read("native-widgets/ios/Sources/Extension/DevHudWidget.swift"),
  read(
    "src-tauri/widget-bridge/ios/Sources/DevHudWidgetPlugin/DevHudWidgetPlugin.swift",
  ),
  read(
    "src-tauri/widget-bridge/ios/Sources/DevHudWidgetCore/DeckNotificationService.swift",
  ),
  read("native-widgets/ios/Support/DevHudWidgetExtension.entitlements"),
  read("native-widgets/ios/project.yml"),
  read("package.json"),
  read("src-tauri/widget-bridge/Cargo.toml"),
  read("src-tauri/widget-bridge/src/lib.rs"),
  read("src-tauri/widget-bridge/src/mobile.rs"),
  read("src/tools/registry.ts"),
  read(
    "src-tauri/widget-bridge/ios/Sources/DevHudWidgetCore/WidgetConfiguration.swift",
  ),
  read(
    "native-widgets/android/shared/src/main/java/dev/deli/devhud/widget/WidgetConfiguration.kt",
  ),
]);

requireCondition(
  iosWidgetProject.includes(
    "PRODUCT_BUNDLE_IDENTIFIER: dev.deli.devhud.widget",
  ) &&
    iosWidgetProject.includes("type: app-extension") &&
    iosWidgetProject.includes("DevHudWidgetExtension.entitlements"),
  "the WidgetKit project must compile the exact widget extension identifier",
);
requireCondition(
  androidPluginSource.includes("@TauriPlugin") &&
    androidPluginSource.includes("class DevHudWidgetPlugin") &&
    androidPluginSource.includes(": Plugin(activity)") &&
    iosPluginSource.includes("final class DevHudWidgetPlugin: Plugin") &&
    iosPluginSource.includes('@_cdecl("init_plugin_devhud_widget")') &&
    privatePluginRustSource.includes("register_android_plugin") &&
    privatePluginRustSource.includes("register_ios_plugin") &&
    privatePluginRustSource.includes('run_mobile_plugin("readConfiguration"') &&
    privatePluginRustSource.includes('run_mobile_plugin("writeConfiguration"') &&
    privatePluginRustSource.includes('run_mobile_plugin("prepareReset"') &&
    privatePluginRustSource.includes('run_mobile_plugin("resetConfiguration"'),
  "widget state operations and reset preflight must cross only the standard private Tauri Kotlin/Swift plugin boundary",
);
requireCondition(
  privatePluginManifest.includes("publish = false") &&
    !privatePluginLibrarySource.includes("invoke_handler") &&
    !privatePluginLibrarySource.includes("global_api_script") &&
    !packageSource.includes("tauri-plugin-devhud-widget"),
  "the native widget bridge must remain private with no JavaScript or public plugin surface",
);
requireCondition(
  swiftConfigurationSource.includes('"devhud.widget-configuration.v1"') &&
    swiftConfigurationSource.includes('"group.dev.deli.devhud"') &&
    kotlinConfigurationSource.includes('"devhud.widget-configuration.v1"') &&
    kotlinConfigurationSource.includes(
      'DATASTORE_NAME = "devhud-widget-state"',
    ) &&
    JSON.parse(fixtureSource).configuration.widgets.length === 1 &&
    JSON.parse(fixtureSource).configuration.widgets[0].snapshot.offline === true &&
    swiftConfigurationSource.includes('case countsOnly = "counts-only"') &&
    kotlinConfigurationSource.includes('COUNTS_ONLY("counts-only")'),
  "both native adapters and the fixture must preserve the minimal account-bound v1 Deck snapshot schema",
);
requireCondition(
  androidProviderSource.includes(": AppWidgetProvider()") &&
    iosExtensionSource.includes("struct DevHudWidget: Widget") &&
    iosExtensionSource.includes(".systemSmall") &&
    iosExtensionSource.includes(".systemMedium") &&
    iosExtensionSource.includes(".systemLarge") &&
    androidProviderSource.includes("DeckWidgetFamily.ANDROID_COMPACT") &&
    androidProviderSource.includes("DeckWidgetFamily.ANDROID_WIDE") &&
    androidProviderSource.includes("DeckWidgetFamily.ANDROID_LIST") &&
    androidProviderSource.includes("snapshot.pullRequests.take(detailLimit)") &&
    androidProviderSource.includes("DeckWidgetAction.Refresh") &&
    iosExtensionSource.includes("DeckWidgetAction.refresh") &&
    !androidProviderSource.includes("MutatePullRequest") &&
    !iosExtensionSource.includes("MutatePullRequest") &&
    !androidProviderSource.includes("fixture-diagnostics") &&
    !iosExtensionSource.includes("fixture-diagnostics"),
  "native targets must render all contracted families and expose only open/refresh Deck widget actions",
);
requireCondition(
  androidProviderInfo.includes(
    'android:configure="dev.deli.devhud.widget.DevHudWidgetConfigurationActivity"',
  ) &&
    androidProviderInfo.includes('android:widgetFeatures="reconfigurable"') &&
    !androidProviderInfo.includes("configuration_optional") &&
    androidPluginManifest.includes("DevHudWidgetConfigurationActivity") &&
    androidPluginManifest.includes("android.appwidget.action.APPWIDGET_CONFIGURE") &&
    androidPluginManifest.includes('android:exported="true"') &&
    androidAppManifest.includes("DevHudWidgetConfigurationActivity") &&
    androidConfigurationActivitySource.includes("DeckWidgetSelections.set") &&
    androidConfigurationActivitySource.includes("setResult(RESULT_OK") &&
    androidProviderSource.includes(
      "widgets.firstOrNull { it.widgetId == storedId && it.family == family }",
    ) &&
    !androidProviderSource.includes("compatible.firstOrNull"),
  "Android widgets must require a narrow reconfigurable per-instance Deck selector without silently choosing the first compatible view",
);
requireCondition(
  androidNotificationSource.includes("setBypassDnd(false)") &&
    androidNotificationSource.includes("IMPORTANCE_DEFAULT") &&
    !androidNotificationSource.includes("IMPORTANCE_HIGH") &&
    iosNotificationSource.includes("content.interruptionLevel = .active") &&
    !iosNotificationSource.includes(".critical") &&
    !iosNotificationSource.includes(".timeSensitive") &&
    swiftConfigurationSource.includes('genericNotificationText = "Deck view updated"') &&
    kotlinConfigurationSource.includes('GENERIC_NOTIFICATION_TEXT = "Deck view updated"') &&
    swiftConfigurationSource.includes('["eventId"]') &&
    kotlinConfigurationSource.includes('payload.keys == setOf("eventId")') &&
    iosNotificationSource.includes("localDetailEnabled") &&
    androidNotificationSource.includes("localDetailEnabled") &&
    androidNotificationSource.includes("DeckNotificationPolicy.text"),
  "native notification policy must preserve DND, exact generic text, and opaque-only payload input",
);
requireCondition(
  iosWidgetEntitlements.includes("group.dev.deli.devhud"),
  "the WidgetKit target must use the exact shared App Group",
);
requireCondition(
  iosAppEntitlements.includes("group.dev.deli.devhud") &&
    !iosAppEntitlements.includes("dev.deli.devhud.widget"),
  "the distributed iOS app may share widget state but must not claim the extension identity",
);
requireCondition(
  iosAppProject.includes("DevHudWidgetExtension:") &&
    iosAppProject.includes("type: app-extension") &&
    iosAppProject.includes("PRODUCT_BUNDLE_IDENTIFIER: dev.deli.devhud.widget") &&
    /dependencies:[\s\S]*?- target: DevHudWidgetExtension[\s\S]*?embed: true/u.test(iosAppProject) &&
  (iosAppProject.split("\ntargets:\n")[1]?.match(
    /^ {2}[A-Za-z0-9_]+:\s*$/gmu,
  ) ?? []).length === 3,
  "the distributed iOS project must compile and embed exactly the app, widget core, and WidgetKit extension targets",
);

requireCondition(
  androidAppManifest.includes('android:name="dev.deli.devhud.widget.DevHudWidgetProvider"') &&
    (androidAppManifest.match(/<receiver\b/gu) ?? []).length === 1 &&
    androidAppManifest.includes("android.appwidget.action.APPWIDGET_UPDATE") &&
    androidAppManifest.includes("android.appwidget.provider") &&
    androidAppManifest.includes('android:exported="false"'),
  "the distributed Android manifest must narrowly register the non-exported Deck app-widget receiver",
);
requireCondition(
  androidPluginManifest.includes("DevHudWidgetProvider") &&
    !/<receiver\b/u.test(androidFoundationManifest) &&
    !androidAppBuild.includes("widget-foundation") &&
    androidFoundationBuild.includes('namespace = "dev.deli.devhud.widget"'),
  "the private bridge module must package the receiver while the independent native test module stays manifest-free",
);
requireCondition(
  JSON.parse(packageSource).version === "0.1.0" &&
    productionRegistry.includes('toolId: "deck"') &&
    productionRegistry.includes("ToolPlatform.Desktop") &&
    productionRegistry.includes("ToolPlatform.Ios") &&
    productionRegistry.includes("ToolPlatform.Android") &&
    productionRegistry.includes('toolId: "realqa"') &&
    productionRegistry.includes(
      "supportedPlatforms: new Set([ToolPlatform.Desktop])",
    ) &&
    /supportedOperatingSystems:\s*new Set\(\[\s*ToolOperatingSystem\.Macos,\s*ToolOperatingSystem\.Ubuntu,\s*ToolOperatingSystem\.Windows,\s*\]\)/u.test(
      productionRegistry,
    ) &&
    (productionRegistry.match(/defineTool\(\{/gu)?.length ?? 0) === 2,
  "DevHud 0.1.0 must register cross-platform Deck and desktop-only RealQA",
);

const prohibitedRemoteEndpoint =
  /https?:\/\/(?!(?:schemas\.android\.com|www\.apple\.com\/DTDs\/))/u;
const prohibitedIosInfoSurface =
  /(?:CFBundleURLTypes|CFBundleURLSchemes)/u;
requireCondition(
  !prohibitedIosInfoSurface.test(iosAppProject) &&
    !prohibitedRemoteEndpoint.test(iosAppProject) &&
    (iosAppProject.match(/com\.apple\.developer\.associated-domains/gu) ?? [])
      .length === 1 &&
    (iosAppProject.match(/applinks:/gu) ?? []).length === 1 &&
    iosAppProject.includes("applinks:deli.dev"),
  "distributed iOS project must contain only the exact DeliDev associated domain",
);
requireCondition(
  hasExactIosAssociatedDomainEntitlement(iosAppEntitlements, true),
  "distributed iOS entitlements must contain only the exact DeliDev associated domain",
);
requireCondition(
  hasExactAndroidLinkSurface(androidAppManifest),
  "distributed Android manifest must contain only the exact verified DeliDev callback and Deck action links",
);

const mergedManifestRoots = [
  "src-tauri/gen/android/app/build/intermediates/merged_manifests",
  "src-tauri/gen/android/app/build/intermediates/packaged_manifests",
];
for (const relativeRoot of mergedManifestRoots) {
  const files = await collectFiles(resolve(appRoot, relativeRoot)).catch(
    (error) => {
      if (error.code === "ENOENT") return [];
      throw error;
    },
  );
  for (const file of files.filter((path) => path.endsWith("AndroidManifest.xml"))) {
    inspectAndroidManifest(await readFile(file, "utf8"), file);
    if (/release/iu.test(file)) androidArtifactsInspected += 1;
  }
}

const arguments_ = process.argv.slice(2);
for (let index = 0; index < arguments_.length; index += 2) {
  const flag = arguments_[index];
  const path = arguments_[index + 1];
  if (!path) throw new Error(`${flag} requires an artifact path.`);
  if (flag === "--android-manifest") {
    inspectAndroidManifest(await readFile(resolve(path), "utf8"), path);
    androidArtifactsInspected += 1;
  } else if (flag === "--android-apk") {
    inspectAndroidManifest(inspectApk(resolve(path)), path);
    androidArtifactsInspected += 1;
  } else if (flag === "--ios-app") {
    await inspectIosApplication(resolve(path));
    iosArtifactsInspected += 1;
  } else {
    throw new Error(`Unknown widget artifact inspection option: ${flag}`);
  }
}

requireCondition(
  androidArtifactsInspected + iosArtifactsInspected > 0,
  "at least one built release Android manifest/APK or iOS application artifact is required",
);

if (failures.length > 0) {
  throw new Error(`DevHud widget artifact checks failed:\n- ${failures.join("\n- ")}`);
}

console.log(
  JSON.stringify({
    androidArtifactsInspected,
    check: "devhud-widget-artifacts",
    iosArtifactsInspected,
    releaseAndroidReceiverRegistered:
      androidArtifactsInspected > 0 ? true : null,
    releaseIosExtensionEmbedded: iosArtifactsInspected > 0 ? true : null,
    status: "passed",
  }),
);

function inspectAndroidManifest(source, path) {
  requireCondition(
    (source.match(/<receiver\b/gu) ?? []).length === 1 &&
      /DevHudWidgetProvider/u.test(source) &&
      /APPWIDGET_UPDATE/u.test(source) &&
      /android\.appwidget\.provider/u.test(source) &&
      /android:exported\s*=\s*["']false["']/u.test(source),
    `distributed Android artifact does not contain the exact non-exported widget receiver: ${path}`,
  );
  requireCondition(
    hasExactAndroidLinkSurface(source),
    `distributed Android artifact does not preserve the exact native auth/action link surface: ${path}`,
  );
  const application = source.match(/<application\b[^>]*>/su)?.[0] ?? "";
  const dataExtractionRules =
    application.match(
      /android:dataExtractionRules\s*=\s*["']([^"']+)["']/u,
    )?.[1] ?? "";
  const fullBackupContent =
    application.match(
      /android:fullBackupContent\s*=\s*["']([^"']+)["']/u,
    )?.[1] ?? "";
  const compiledReference = /^@ref\/0x[0-9a-f]+$/iu;
  const isApk = extname(path).toLowerCase() === ".apk";
  requireCondition(
    /android:allowBackup\s*=\s*["']false["']/u.test(application) &&
      (dataExtractionRules === "@xml/data_extraction_rules" ||
        (isApk && compiledReference.test(dataExtractionRules))) &&
      (fullBackupContent === "@xml/backup_rules" ||
        (isApk && compiledReference.test(fullBackupContent))),
    `distributed Android artifact does not preserve backup exclusions: ${path}`,
  );
}

function inspectApk(path) {
  const executable = findExecutable("apkanalyzer");
  if (!executable) {
    throw new Error(
      "apkanalyzer is required to inspect a supplied Android APK artifact.",
    );
  }
  return execFileSync(executable, ["manifest", "print", path], {
    encoding: "utf8",
  });
}

async function inspectIosApplication(path) {
  await access(path);
  const files = await collectFiles(path);
  const infoPlist = resolve(path, "Info.plist");
  const infoSource = readPropertyList(infoPlist);
  requireCondition(
    !prohibitedIosInfoSurface.test(infoSource) &&
      !prohibitedRemoteEndpoint.test(infoSource),
    `distributed iOS application contains prohibited Info.plist metadata: ${infoPlist}`,
  );
  const extensionFiles = files.filter((file) =>
    file.includes("/PlugIns/DevHudWidgetExtension.appex/") ||
    file.includes("\\PlugIns\\DevHudWidgetExtension.appex\\")
  );
  requireCondition(
    extensionFiles.some((file) => file.endsWith("Info.plist")) &&
      extensionFiles.some((file) => file.includes("DevHudWidgetExtension")),
    `distributed iOS application does not embed the Deck WidgetKit extension: ${path}`,
  );
  for (const file of files.filter(
    (candidate) =>
      candidate.endsWith("embedded.mobileprovision") ||
      candidate.endsWith(".xcent") ||
      candidate.endsWith(".entitlements"),
  )) {
    const source = file.endsWith("embedded.mobileprovision")
      ? (await readFile(file)).toString("latin1")
      : readPropertyList(file);
    const extensionPayload = file.includes("/PlugIns/") || file.includes("\\PlugIns\\");
    requireCondition(
      extensionPayload
        ? source.includes("dev.deli.devhud.widget") && source.includes("group.dev.deli.devhud")
        : !source.includes("dev.deli.devhud.widget") &&
          hasExactIosAssociatedDomainEntitlement(source, true) &&
          source.includes("group.dev.deli.devhud"),
      `distributed iOS provisioning payload does not preserve its narrow app/widget identity: ${file}`,
    );
  }
  if (existsSync(resolve(path, "_CodeSignature"))) {
    const entitlements = readCodeSignatureEntitlements(path);
    requireCondition(
      !entitlements.includes("dev.deli.devhud.widget"),
      `distributed iOS code signature claims the widget extension: ${path}`,
    );
    requireCondition(
      hasExactIosAssociatedDomainEntitlement(entitlements, true),
      `distributed iOS code signature does not preserve the exact DeliDev associated domain: ${path}`,
    );
  }
}

function hasExactAndroidLinkSurface(source) {
  return (
    !prohibitedRemoteEndpoint.test(source) &&
    (source.match(/android\.permission\.INTERNET/gu) ?? []).length === 1 &&
    (source.match(/android\.intent\.category\.BROWSABLE/gu) ?? []).length === 2 &&
    (source.match(/android:autoVerify\s*=\s*["']true["']/gu) ?? []).length === 2 &&
    (source.match(/android:scheme\s*=/gu) ?? []).length === 2 &&
    (source.match(/android:scheme\s*=\s*["']https["']/gu) ?? []).length === 2 &&
    (source.match(/android:host\s*=/gu) ?? []).length === 2 &&
    (source.match(/android:host\s*=\s*["']deli\.dev["']/gu) ?? []).length === 2 &&
    (source.match(/android:path\s*=/gu) ?? []).length === 2 &&
    /android:path\s*=\s*["']\/auth\/devhud\/callback["']/u.test(source) &&
    /android:path\s*=\s*["']\/devhud\/deck\/open["']/u.test(source) &&
    !/android:path(?:Prefix|Pattern)\s*=/u.test(source)
  );
}

function hasExactIosAssociatedDomainEntitlement(source, required) {
  if (
    prohibitedIosInfoSurface.test(source) ||
    prohibitedRemoteEndpoint.test(source)
  ) {
    return false;
  }
  const arrays = [
    ...source.matchAll(
      /<key>com\.apple\.developer\.associated-domains<\/key>\s*<array>([\s\S]*?)<\/array>/gu,
    ),
  ];
  if (arrays.length === 0) {
    return !required && !source.includes("applinks:");
  }
  const domains = arrays.flatMap(([, contents]) =>
    [...contents.matchAll(/<string>([^<]+)<\/string>/gu)].map(
      ([, domain]) => domain,
    ),
  );
  return (
    arrays.length === 1 &&
    domains.length === 1 &&
    domains[0] === "applinks:deli.dev"
  );
}

function readPropertyList(path) {
  const executable = findExecutable("plutil");
  if (!executable) {
    throw new Error("plutil is required to inspect an iOS application artifact.");
  }
  return execFileSync(executable, ["-convert", "xml1", "-o", "-", path], {
    encoding: "utf8",
  });
}

function readCodeSignatureEntitlements(path) {
  const executable = findExecutable("codesign");
  if (!executable) {
    throw new Error(
      "codesign is required to inspect a signed iOS application artifact.",
    );
  }
  return execFileSync(
    executable,
    ["--display", "--entitlements", "-", "--xml", path],
    { encoding: "utf8" },
  );
}

function findExecutable(name) {
  const extension = process.platform === "win32" ? ".bat" : "";
  const sdkRoots = [
    process.env.ANDROID_HOME,
    process.env.ANDROID_SDK_ROOT,
  ].filter(Boolean);
  const directories = [
    ...(process.env.PATH ?? "").split(delimiter).filter(Boolean),
    ...sdkRoots.map((root) => resolve(root, "cmdline-tools", "latest", "bin")),
  ];
  for (const directory of directories) {
    const candidate = resolve(directory, `${name}${extension}`);
    if (existsSync(candidate)) return candidate;
  }
  return null;
}
