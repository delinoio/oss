import { mkdir, readFile, unlink, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const nativeRoot = resolve(appRoot, "src-tauri");
const platform = process.argv[2];
const pinnedTauriDependency =
  'tauri = { git = "https://github.com/tauri-apps/tauri", rev = "f49ebda2fdba5755456b0f049e32593ca0ea331a", default-features = false, optional = true }';
const gradleDistributionSha256 =
  "bd71102213493060956ec229d946beee57158dbd89d0e62b91bca0fa2c5f3531";
const backupDomains = [
  "root",
  "file",
  "database",
  "sharedpref",
  "external",
  "device_root",
  "device_file",
  "device_database",
  "device_sharedpref",
];

function exclusions(indent) {
  return backupDomains
    .map((domain) => `${indent}<exclude domain="${domain}" path="." />`)
    .join("\n");
}

const backupRules = `<?xml version="1.0" encoding="utf-8"?>
<full-backup-content>
${exclusions("    ")}
</full-backup-content>
`;
const dataExtractionRules = `<?xml version="1.0" encoding="utf-8"?>
<data-extraction-rules>
    <cloud-backup>
${exclusions("        ")}
    </cloud-backup>
    <device-transfer>
${exclusions("        ")}
    </device-transfer>
</data-extraction-rules>
`;

async function update(relativePath, transform) {
  const path = resolve(nativeRoot, relativePath);
  const source = await readFile(path, "utf8");
  const updated = transform(source);
  if (updated === source) return;
  await writeFile(path, updated);
}

await update("Cargo.toml", (source) => {
  let updated = source
    .replace(
      /(\[target\.'cfg\(any\(target_os = "android", target_os = "ios"\)\)'\.dependencies\]\s+)tauri\s*=\s*\{[^\n]+\}/u,
      '$1tauri-runtime-wry = { git = "https://github.com/tauri-apps/tauri", rev = "f49ebda2fdba5755456b0f049e32593ca0ea331a", default-features = false, optional = true }',
    )
    .replace(
      'mobile-system-webview = ["dep:tauri"]',
      'mobile-system-webview = ["dep:tauri", "dep:tauri-runtime-wry"]',
    );

  const dependenciesSection =
    /(\[dependencies\][\s\S]*?)(?=\n\[target\.|\n\[features\])/u;
  const dependencies = updated.match(dependenciesSection)?.[1] ?? "";

  if (/^tauri\s*=/mu.test(dependencies)) {
    updated = updated.replace(
      dependenciesSection,
      (section) =>
        section.replace(/^tauri\s*=\s*\{[^\n]+\}$/mu, pinnedTauriDependency),
    );
  } else {
    updated = updated.replace(
      /(\[dependencies\][\s\S]*?url\s*=\s*"2"\s*)/u,
      `$1\n${pinnedTauriDependency}\n`,
    );
  }

  return updated;
});

if (platform === "android") {
  await update("gen/android/app/src/main/AndroidManifest.xml", (source) => {
    let updated = source
      .replace(
        /\s*<uses-permission android:name="android\.permission\.INTERNET" \/>\s*/u,
        "\n",
      )
      .replace(
        /\s*<!-- AndroidTV support -->\s*<uses-feature[^>]+android\.software\.leanback[^>]+\/>\s*/u,
        "\n",
      )
      .replace(
        'android:usesCleartextTraffic="${usesCleartextTraffic}"',
        'android:usesCleartextTraffic="false"',
      )
      .replace(
        /\s*<!-- AndroidTV support -->\s*<category android:name="android\.intent\.category\.LEANBACK_LAUNCHER" \/>\s*/u,
        "\n",
      )
      .replace(/\s*<provider[\s\S]*?<\/provider>\s*/u, "\n");
    if (!updated.includes('xmlns:tools="http://schemas.android.com/tools"')) {
      updated = updated.replace(
        'xmlns:android="http://schemas.android.com/apk/res/android"',
        'xmlns:android="http://schemas.android.com/apk/res/android"\n    xmlns:tools="http://schemas.android.com/tools"',
      );
    }
    if (!updated.includes('<receiver tools:node="removeAll" />')) {
      updated = updated.replace(
        /(<application\b[^>]*>)/su,
        '$1\n        <!-- The distributed application intentionally exposes no broadcast receivers. -->\n        <receiver tools:node="removeAll" />',
      );
    }
    for (const [attribute, value] of [
      ["allowBackup", "false"],
      ["dataExtractionRules", "@xml/data_extraction_rules"],
      ["fullBackupContent", "@xml/backup_rules"],
    ]) {
      const pattern = new RegExp(`android:${attribute}="[^"]*"`, "u");
      updated = pattern.test(updated)
        ? updated.replace(pattern, `android:${attribute}="${value}"`)
        : updated.replace(
            /<application\b/u,
            `<application\n        android:${attribute}="${value}"`,
          );
    }
    return updated;
  });
  await update("gen/android/app/build.gradle.kts", (source) => {
    let updated = source
      .replace(
        /^\s*manifestPlaceholders\["usesCleartextTraffic"\].*\n/gmu,
        "",
      )
      .replace(
        "packaging {                jniLibs",
        "packaging {\n                jniLibs",
      )
      .replace(
        /^\s*versionName = [^\n]+$/mu,
        '        versionName = "0.1.0+f49ebda2fdba5755456b0f049e32593ca0ea331a"',
      );
    if (!updated.includes("abiFilters +=")) {
      updated = updated.replace(
        /(\s*versionName = [^\n]+\n)/u,
        '$1        ndk {\n            abiFilters += listOf("arm64-v8a", "armeabi-v7a", "x86_64")\n        }\n',
      );
    }
    return updated;
  });
  await update("gen/android/gradle.properties", (source) => {
    let updated = source.replace(
      /^org\.gradle\.jvmargs=.*$/mu,
      "org.gradle.jvmargs=-Xmx1024m -XX:MaxMetaspaceSize=512m -Dfile.encoding=UTF-8",
    );
    if (!updated.includes("org.gradle.daemon=false")) {
      updated = updated.replace(
        /^(org\.gradle\.jvmargs=.*)$/mu,
        "$1\norg.gradle.daemon=false\norg.gradle.workers.max=2",
      );
    }
    return updated;
  });
  await update("gen/android/gradle/wrapper/gradle-wrapper.properties", (source) => {
    if (/^distributionSha256Sum=/mu.test(source)) {
      return source.replace(
        /^distributionSha256Sum=.*$/mu,
        `distributionSha256Sum=${gradleDistributionSha256}`,
      );
    }
    return source.replace(
      /^(distributionUrl=.*)$/mu,
      `$1\ndistributionSha256Sum=${gradleDistributionSha256}`,
    );
  });
  await update(
    "gen/android/buildSrc/src/main/java/dev/deli/devhud/kotlin/BuildTask.kt",
    (source) =>
      source
        .replace('val executable = """pnpm""";', 'val executable = """node""";')
        .replace(
          'val args = listOf("tauri", "android", "android-studio-script");',
          'val args = listOf(\n            "../node_modules/@tauri-apps/cli-mobile/tauri.js",\n            "android",\n            "android-studio-script",\n        );',
        ),
  );
  await unlink(
    resolve(
      nativeRoot,
      "gen/android/app/src/main/res/xml/file_paths.xml",
    ),
  ).catch((error) => {
    if (error.code !== "ENOENT") throw error;
  });
  const backupRulesDirectory = resolve(
    nativeRoot,
    "gen/android/app/src/main/res/xml",
  );
  await mkdir(backupRulesDirectory, { recursive: true });
  await Promise.all([
    writeFile(resolve(backupRulesDirectory, "backup_rules.xml"), backupRules),
    writeFile(
      resolve(backupRulesDirectory, "data_extraction_rules.xml"),
      dataExtractionRules,
    ),
  ]);
  for (const relativePath of [
    "gen/android/build.gradle.kts",
    "gen/android/buildSrc/build.gradle.kts",
    "gen/android/buildSrc/src/main/java/dev/deli/devhud/kotlin/BuildTask.kt",
  ]) {
    await update(relativePath, (source) =>
      source.replace(/[ \t]+$/gmu, "").replace(/\n+$/u, "\n"),
    );
  }
} else {
  await update("gen/apple/project.yml", (source) => source.includes("DevHudTauriRevision")
    ? source
    : source.replace('        CFBundleVersion: "1"', '        CFBundleVersion: "1"\n        DevHudTauriRevision: f49ebda2fdba5755456b0f049e32593ca0ea331a'));
}

if (platform === "ios") {
  for (const relativePath of [
    "gen/apple/project.yml",
    "gen/apple/devhud.xcodeproj/project.pbxproj",
  ]) {
    await update(relativePath, (source) =>
      source.replaceAll(
        "pnpm tauri ios xcode-script",
        "node ../../../node_modules/@tauri-apps/cli-mobile/tauri.js ios xcode-script",
      ),
    ).catch((error) => {
      if (error.code !== "ENOENT") throw error;
    });
  }
  await update(
    "gen/apple/devhud_iOS/devhud_iOS.entitlements",
    (source) => {
      if (source.includes("group.dev.deli.devhud")) return source;
      const entitlement =
        "\t<key>com.apple.security.application-groups</key>\n" +
        "\t<array>\n" +
        "\t\t<string>group.dev.deli.devhud</string>\n" +
        "\t</array>";
      return source.includes("<dict/>")
        ? source.replace("<dict/>", `<dict>\n${entitlement}\n</dict>`)
        : source.replace("<dict>", `<dict>\n${entitlement}`);
    },
  );
}
