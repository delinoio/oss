function assert(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

export function resolvedPackageClosure(metadata, rootPackageId) {
  assert(metadata.resolve, "Cargo metadata did not include a resolved dependency graph");

  const packagesById = new Map(metadata.packages.map((pkg) => [pkg.id, pkg]));
  const nodesById = new Map(metadata.resolve.nodes.map((node) => [node.id, node]));
  const packageIds = new Set();
  const pending = [rootPackageId];

  while (pending.length > 0) {
    const packageId = pending.pop();
    if (packageIds.has(packageId)) {
      continue;
    }

    assert(packagesById.has(packageId), `Cargo metadata is missing package ${packageId}`);
    const node = nodesById.get(packageId);
    assert(node, `Cargo metadata is missing resolve node ${packageId}`);
    packageIds.add(packageId);
    pending.push(...node.dependencies);
  }

  return { nodesById, packageIds, packagesById };
}

export function validateDependencyPresence(
  metadata,
  rootPackageId,
  dependencyName,
  expected,
  target,
) {
  const closure = resolvedPackageClosure(metadata, rootPackageId);
  const present = [...closure.packageIds].some(
    (packageId) => closure.packagesById.get(packageId)?.name === dependencyName,
  );
  assert(
    present === expected,
    `${dependencyName} ${expected ? "is missing from" : "must be absent from"} ${target}`,
  );
}

export function validateResolvedDependencySources(
  metadata,
  rootPackageId,
  allowedSources,
  allowedLocalPackageIds = new Set(),
) {
  const closure = resolvedPackageClosure(metadata, rootPackageId);

  for (const packageId of closure.packageIds) {
    if (packageId === rootPackageId) {
      continue;
    }

    const pkg = closure.packagesById.get(packageId);
    if (pkg.source === null && allowedLocalPackageIds.has(packageId)) {
      continue;
    }
    assert(
      allowedSources.has(pkg.source),
      `forbidden source in the DevHUD dependency graph: ${pkg.name} ${pkg.version} (${pkg.source ?? "local path"})`,
    );
  }

  return closure;
}

export function validateCiTargetMatrix(workflow, targets) {
  const entries = workflow.jobs?.["devhud-desktop"]?.strategy?.matrix?.include;
  assert(Array.isArray(entries), "native CI matrix include list is missing");
  assert(
    entries.length === targets.length,
    "native CI matrix must contain exactly one entry per desktop target",
  );

  const entriesById = new Map();
  for (const entry of entries) {
    assert(
      typeof entry?.id === "string" && !entriesById.has(entry.id),
      `native CI matrix contains a duplicate or invalid target ID: ${entry?.id ?? "missing"}`,
    );
    entriesById.set(entry.id, entry);
  }

  for (const { id, runner } of targets) {
    const entry = entriesById.get(id);
    assert(entry, `native CI matrix is missing ${id}`);
    assert(entry.runner === runner, `native CI matrix runner changed for ${id}`);
  }
}
