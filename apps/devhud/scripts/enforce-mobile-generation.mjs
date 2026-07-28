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
    if (!updated.includes('android:path="/auth/devhud/callback"')) {
      updated = updated.replace(
        /(\s*<\/activity>)/u,
        `            <intent-filter android:autoVerify="true">
                <action android:name="android.intent.action.VIEW" />
                <category android:name="android.intent.category.DEFAULT" />
                <category android:name="android.intent.category.BROWSABLE" />
                <data
                    android:host="deli.dev"
                    android:path="/auth/devhud/callback"
                    android:scheme="https" />
            </intent-filter>$1`,
      );
    }
    for (const [attribute, value] of [
      ["allowBackup", "false"],
      ["dataExtractionRules", "@xml/data_extraction_rules"],
      ["fullBackupContent", "@xml/backup_rules"],
      ["roundIcon", "@mipmap/ic_launcher_round"],
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
}

if (platform === "ios") {
  await update("gen/apple/project.yml", (source) => {
    let updated = source;
    if (!updated.includes("- path: Assets.xcassets")) {
      updated = updated.replace(
        /(^\s+sources:\n)(\s+- path: Sources\n)/mu,
        "$1$2      - path: Assets.xcassets\n",
      );
    }
    if (!updated.includes("ASSETCATALOG_COMPILER_APPICON_NAME: AppIcon")) {
      updated = updated.replace(
        /(^\s+settings:\n\s+base:\n)/mu,
        "$1        ASSETCATALOG_COMPILER_APPICON_NAME: AppIcon\n",
      );
    }
    if (!updated.includes("com.apple.developer.associated-domains")) {
      updated = updated.replace(
        /(\s+com\.apple\.security\.application-groups:\n\s+- group\.dev\.deli\.devhud\n)/u,
        "$1        com.apple.developer.associated-domains:\n          - applinks:deli.dev\n",
      );
    }
    return updated;
  });
  await writeFile(
    resolve(nativeRoot, "gen/apple/LaunchScreen.storyboard"),
    `<?xml version="1.0" encoding="UTF-8"?>
<document type="com.apple.InterfaceBuilder3.CocoaTouch.Storyboard.XIB" version="3.0" toolsVersion="17150" targetRuntime="iOS.CocoaTouch" propertyAccessControl="none" useAutolayout="YES" useTraitCollections="YES" useSafeAreas="YES" colorMatched="YES" initialViewController="Y6W-OH-hqX">
    <dependencies>
        <plugIn identifier="com.apple.InterfaceBuilder.IBCocoaTouchPlugin" version="17122"/>
        <capability name="Safe area layout guides" minToolsVersion="9.0"/>
        <capability name="System colors in document resources" minToolsVersion="11.0"/>
        <capability name="documents saved in the Xcode 8 format" minToolsVersion="8.0"/>
    </dependencies>
    <scenes>
        <scene sceneID="s0d-6b-0kx">
            <objects>
                <viewController id="Y6W-OH-hqX" sceneMemberID="viewController">
                    <view key="view" contentMode="scaleToFill" id="5EZ-qb-Rvc">
                        <rect key="frame" x="0.0" y="0.0" width="414" height="896"/>
                        <autoresizingMask key="autoresizingMask" widthSizable="YES" heightSizable="YES"/>
                        <viewLayoutGuide key="safeArea" id="vDu-zF-Fre"/>
                        <color key="backgroundColor" systemColor="systemBackgroundColor"/>
                        <subviews>
                            <imageView opaque="NO" clipsSubviews="YES" userInteractionEnabled="NO" contentMode="scaleAspectFit" image="LaunchLogo" translatesAutoresizingMaskIntoConstraints="NO" id="DH-logo">
                                <rect key="frame" x="157" y="346" width="100" height="100"/>
                                <constraints>
                                    <constraint firstAttribute="width" constant="100" id="DH-logo-width"/>
                                    <constraint firstAttribute="height" constant="100" id="DH-logo-height"/>
                                </constraints>
                            </imageView>
                        </subviews>
                        <constraints>
                            <constraint firstItem="DH-logo" firstAttribute="centerX" secondItem="5EZ-qb-Rvc" secondAttribute="centerX" id="DH-logo-center-x"/>
                            <constraint firstItem="DH-logo" firstAttribute="centerY" secondItem="5EZ-qb-Rvc" secondAttribute="centerY" id="DH-logo-center-y"/>
                        </constraints>
                    </view>
                </viewController>
                <placeholder placeholderIdentifier="IBFirstResponder" id="Ief-a0-LHa" userLabel="First Responder" customClass="UIResponder" sceneMemberID="firstResponder"/>
            </objects>
        </scene>
    </scenes>
    <resources>
        <image name="LaunchLogo" width="512" height="512"/>
        <systemColor name="systemBackgroundColor">
            <color white="1" alpha="1" colorSpace="custom" customColorSpace="genericGamma22GrayColorSpace"/>
        </systemColor>
    </resources>
</document>
`,
  );
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
      let updated = source;
      if (!updated.includes("group.dev.deli.devhud")) {
        const entitlement =
          "\t<key>com.apple.security.application-groups</key>\n" +
          "\t<array>\n" +
          "\t\t<string>group.dev.deli.devhud</string>\n" +
          "\t</array>";
        updated = updated.includes("<dict/>")
          ? updated.replace("<dict/>", `<dict>\n${entitlement}\n</dict>`)
          : updated.replace("<dict>", `<dict>\n${entitlement}`);
      }
      if (!updated.includes("applinks:deli.dev")) {
        const associatedDomains =
          "\t<key>com.apple.developer.associated-domains</key>\n" +
          "\t<array>\n" +
          "\t\t<string>applinks:deli.dev</string>\n" +
          "\t</array>";
        updated = updated.replace("</dict>", `${associatedDomains}\n</dict>`);
      }
      return updated;
    },
  );
}
