// SPDX-License-Identifier: Apache-2.0
// Rspack discovers committed JSON catalogs at build time. Absent target catalogs
// deliberately use English until the maintainer runs plan-backed generation.
declare const require: {
  context(
    directory: string,
    recursive: boolean,
    pattern: RegExp,
    mode: "lazy",
  ): {
    keys(): string[];
    (key: string): Promise<{ default: Record<string, string> }>;
  };
};
const catalogs = require.context("../i18n/messages", false, /\.json$/, "lazy");
export async function loadCatalog(
  language: string,
): Promise<Record<string, string> | undefined> {
  const key = `./${language}.json`;
  return catalogs.keys().includes(key)
    ? (await catalogs(key)).default
    : undefined;
}
