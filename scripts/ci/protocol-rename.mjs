// SPDX-License-Identifier: Apache-2.0

// Permit only the intentional AIPRouter -> TaiRouter namespace migration.
// Keep descriptor contents intact so FILE checks still reject deleted fields,
// methods and files. Remove this projection when supported baselines no longer
// contain the previous package.
export function projectTaiRouterRename(baseline, current) {
  const previousPackage = "aiprouter.v1";
  const nextPackage = "tairouter.v1";
  const previousGo = "github.com/delinoio/oss/protos/aiprouter/v1;aiprouterv1";
  const nextGo = "github.com/delinoio/oss/protos/tairouter/v1;tairouterv1";
  const renamed = new Map();
  for (const file of baseline.file) {
    if (
      file.package !== previousPackage ||
      !file.name.startsWith("aiprouter/v1/") ||
      file.options?.goPackage !== previousGo
    )
      continue;
    const name = file.name.replace(/^aiprouter\//u, "tairouter/");
    if (
      !current.file.some(
        (candidate) =>
          candidate.name === name &&
          candidate.package === nextPackage &&
          candidate.options?.goPackage === nextGo,
      )
    )
      continue;
    renamed.set(file.name, name);
    file.name = name;
    file.package = nextPackage;
    file.options.goPackage = nextGo;
  }
  if (!renamed.size) return;
  const typeKeys = new Set(["typeName", "extendee", "inputType", "outputType"]);
  const visit = (value) => {
    if (!value || typeof value !== "object") return;
    for (const [key, child] of Object.entries(value)) {
      if (
        typeKeys.has(key) &&
        typeof child === "string" &&
        child.startsWith(`.${previousPackage}.`)
      ) {
        value[key] =
          `.${nextPackage}.${child.slice(previousPackage.length + 2)}`;
      } else visit(child);
    }
  };
  for (const file of baseline.file) {
    if (file.dependency)
      file.dependency = file.dependency.map(
        (name) => renamed.get(name) ?? name,
      );
    visit(file);
  }
}
