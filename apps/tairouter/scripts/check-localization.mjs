// SPDX-License-Identifier: Apache-2.0
import { access } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { join } from "node:path";
import {
  inspect,
  loadProject,
} from "../../../scripts/localization/catalog.mjs";

export async function checkLocalization(root) {
  const project = await loadProject(root);
  const deferred = [];
  const available = [];
  for (const locale of project.locales) {
    try {
      await access(join(root, "i18n/messages", `${locale.lang}.json`));
      available.push(locale);
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
      // Once generation has started, removing its catalog must fail validation.
      // No target catalogs or fingerprints are fabricated for deferred translations.
      if (Object.keys(project.state[locale.lang] ?? {}).length)
        available.push(locale);
      else deferred.push(locale.lang);
    }
  }
  const { issues } = await inspect({ ...project, locales: available });
  if (issues.length)
    throw new Error(
      `${issues.join("\n")}\nRun pnpm i18n:translate and review the changes.`,
    );
  return { event: "translations_checked", result: "passed", deferred };
}
if (
  process.argv[1] &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  try {
    console.log(JSON.stringify(await checkLocalization(process.cwd())));
  } catch (error) {
    console.error(
      JSON.stringify({ event: "localization_failed", message: error.message }),
    );
    process.exitCode = 1;
  }
}
