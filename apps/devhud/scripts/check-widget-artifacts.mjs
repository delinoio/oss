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
  androidProviderSource,
  fixtureSource,
  iosAppEntitlements,
  iosAppProject,
  iosExtensionSource,
  iosPluginSource,
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
    "native-widgets/android/widget-foundation/src/main/java/dev/deli/devhud/widget/DevHudWidgetProvider.kt",
  ),
  read("native-widgets/fixtures/widget-configuration.v1.json"),
  read("src-tauri/gen/apple/devhud_iOS/devhud_iOS.entitlements"),
  read("src-tauri/gen/apple/project.yml"),
  read("native-widgets/ios/Sources/Extension/DevHudWidget.swift"),
  read(
    "src-tauri/widget-bridge/ios/Sources/DevHudWidgetPlugin/DevHudWidgetPlugin.swift",
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
  "the build-only iOS project must compile the exact widget extension identifier",
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
    privatePluginRustSource.includes('run_mobile_plugin("resetConfiguration"'),
  "widget state operations must cross only the standard private Tauri Kotlin/Swift plugin boundary",
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
    JSON.stringify(JSON.parse(fixtureSource)) ===
      JSON.stringify({
        version: 1,
        configuration: {
          slots: [{ slot: "primary", toolId: "fixture-diagnostics" }],
        },
      }),
  "both native adapters and the fixture must preserve the exact v1 schema and stable toolId reference",
);
requireCondition(
  androidProviderSource.includes(": AppWidgetProvider()") &&
    iosExtensionSource.includes("struct DevHudWidget: Widget") &&
    !androidProviderSource.includes("fixture-diagnostics") &&
    !iosExtensionSource.includes("fixture-diagnostics"),
  "build-only native targets must compile without presenting a fixture tool",
);
requireCondition(
  iosWidgetEntitlements.includes("group.dev.deli.devhud"),
  "the build-only WidgetKit target must use the future shared App Group",
);
requireCondition(
  iosAppEntitlements.includes("group.dev.deli.devhud") &&
    !iosAppEntitlements.includes("dev.deli.devhud.widget"),
  "the distributed iOS app may share widget state but must not claim the extension identity",
);
requireCondition(
  !/(WidgetKit|app-extension|\.appex|dev\.deli\.devhud\.widget)/u.test(
    iosAppProject,
  ),
  "the distributed iOS XcodeGen project must not contain or embed the WidgetKit target",
);
requireCondition(
  (iosAppProject.split("\ntargets:\n")[1]?.match(
    /^ {2}[A-Za-z0-9_]+:\s*$/gmu,
  ) ?? []).length === 1,
  "the distributed iOS project must contain exactly one application target",
);

for (const [name, manifest] of [
  ["distributed Android", androidAppManifest],
  ["private Android plugin", androidPluginManifest],
  ["build-only Android foundation", androidFoundationManifest],
]) {
  requireCondition(
    !/<receiver\b|APPWIDGET_UPDATE|android\.appwidget\.provider|AppWidgetProvider/u.test(
      manifest,
    ),
    `${name} manifest must not register an app-widget receiver`,
  );
}
requireCondition(
  !androidAppBuild.includes("widget-foundation") &&
    androidFoundationBuild.includes('namespace = "dev.deli.devhud.widget.foundation"'),
  "the distributed Android app must not depend on the build-only widget module",
);
requireCondition(
  JSON.parse(packageSource).version === "0.1.0" &&
    /export const productionTools:\s*readonly ToolDefinition\[\]\s*=\s*\[\];/u.test(
      productionRegistry,
    ),
  "DevHud 0.1.0 must keep production tools empty",
);

const prohibitedReleaseSurface =
  /(?:CFBundleURLSchemes|com\.apple\.developer\.associated-domains|android\.intent\.category\.BROWSABLE|android:scheme=|android:autoVerify|https?:\/\/(?!(?:schemas\.android\.com|www\.apple\.com\/DTDs\/)))/u;
for (const [name, source] of [
  ["distributed iOS project", iosAppProject],
  ["distributed iOS entitlements", iosAppEntitlements],
  ["distributed Android manifest", androidAppManifest],
]) {
  requireCondition(
    !prohibitedReleaseSurface.test(source),
    `${name} contains a deep link, associated domain, or remote endpoint`,
  );
}

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
      androidArtifactsInspected > 0 ? false : null,
    releaseIosExtensionEmbedded: iosArtifactsInspected > 0 ? false : null,
    status: "passed",
  }),
);

function inspectAndroidManifest(source, path) {
  requireCondition(
    !/(APPWIDGET_UPDATE|android\.appwidget\.provider|DevHudWidgetProvider|dev\.deli\.devhud\.widget)/u.test(
      source,
    ),
    `distributed Android artifact registers a widget receiver: ${path}`,
  );
  requireCondition(
    !/(android\.intent\.category\.BROWSABLE|android:scheme=|android:autoVerify|android\.permission\.INTERNET)/u.test(
      source,
    ),
    `distributed Android artifact contains a prohibited release surface: ${path}`,
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
  requireCondition(
    !files.some(
      (file) =>
        extname(file) === ".appex" ||
        file.includes("/PlugIns/") ||
        file.includes("\\PlugIns\\"),
    ),
    `distributed iOS application embeds an extension: ${path}`,
  );
  for (const file of files.filter(
    (candidate) =>
      candidate.endsWith("embedded.mobileprovision") ||
      candidate.endsWith(".xcent") ||
      candidate.endsWith(".entitlements"),
  )) {
    const source = await readFile(file);
    requireCondition(
      !source.toString("latin1").includes("dev.deli.devhud.widget"),
      `distributed iOS provisioning payload claims the widget extension: ${file}`,
    );
  }
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
