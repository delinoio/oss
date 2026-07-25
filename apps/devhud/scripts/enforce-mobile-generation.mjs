import { readFile, unlink, writeFile } from "node:fs/promises";
import { resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const nativeRoot = resolve(appRoot, "src-tauri");
const platform = process.argv[2];
const pinnedTauriDependency =
  'tauri = { git = "https://github.com/tauri-apps/tauri", rev = "f49ebda2fdba5755456b0f049e32593ca0ea331a", default-features = false, optional = true }';

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
  await update("gen/android/app/src/main/AndroidManifest.xml", (source) =>
    source
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
      .replace(/\s*<provider[\s\S]*?<\/provider>\s*/u, "\n"),
  );
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
}
