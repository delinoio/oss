"use server";

import { revalidatePath } from "next/cache";
import { redirect } from "next/navigation";
import { activateVersion, getPolicy, setPolicy, type PolicyBinding, type ThenvScope } from "@/server/thenv-api";
import { normalizeRoleValue } from "@/server/thenv-normalize";

function resolveScopeFromForm(formData: FormData): ThenvScope {
  return {
    workspaceId: String(formData.get("workspace") ?? "default-workspace").trim(),
    projectId: String(formData.get("project") ?? "default-project").trim(),
    environmentId: String(formData.get("env") ?? "dev").trim(),
  };
}

function redirectToScope(scope: ThenvScope): never {
  const query = new URLSearchParams({
    workspace: scope.workspaceId,
    project: scope.projectId,
    env: scope.environmentId,
  });
  redirect(`/apps/thenv?${query.toString()}`);
}

export async function activateVersionAction(formData: FormData): Promise<void> {
  const scope = resolveScopeFromForm(formData);
  const bundleVersionId = String(formData.get("bundleVersionId") ?? "").trim();
  if (bundleVersionId === "") {
    redirectToScope(scope);
  }

  await activateVersion(scope, bundleVersionId);
  revalidatePath("/apps/thenv");
  redirectToScope(scope);
}

export async function upsertPolicyBindingAction(formData: FormData): Promise<void> {
  const scope = resolveScopeFromForm(formData);
  const subject = String(formData.get("subject") ?? "").trim();
  const role = normalizeRoleValue(String(formData.get("role") ?? "reader"));

  if (subject === "") {
    redirectToScope(scope);
  }

  const existingPolicy = await getPolicy(scope);
  const bindingMap = new Map<string, PolicyBinding>();

  for (const binding of existingPolicy.bindings) {
    bindingMap.set(binding.subject, binding);
  }

  bindingMap.set(subject, { subject, role });

  await setPolicy(scope, Array.from(bindingMap.values()));
  revalidatePath("/apps/thenv");
  redirectToScope(scope);
}
