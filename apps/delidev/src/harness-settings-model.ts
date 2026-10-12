// SPDX-License-Identifier: Apache-2.0
import { items, object, type Document } from "./documents";

export enum HarnessScopeSource {
  Any = "any",
  Api = "api",
  Subscription = "subscription",
}
export enum HarnessSettingMode {
  Inherit = "inherit",
  Override = "override",
}
export enum HarnessSettingsOwner {
  Server = "server",
  Project = "project",
  Agent = "agent",
}
export interface HarnessScope {
  harness: "codex" | "claude-code";
  source: HarnessScopeSource;
  provider_id?: string;
  subscription_service?: string;
  api_protocol?: string;
}
export const codexScope: HarnessScope = {
  harness: "codex",
  source: HarnessScopeSource.Any,
};
export const claudeScope: HarnessScope = {
  harness: "claude-code",
  source: HarnessScopeSource.Any,
};
export function harnessScopeKey(scope: HarnessScope): string {
  return JSON.stringify([
    scope.harness,
    scope.source,
    scope.provider_id ?? "",
    scope.subscription_service ?? "",
    scope.api_protocol ?? "",
  ]);
}
function rank(scope: Document): number {
  return scope.api_protocol
    ? 3
    : scope.provider_id || scope.subscription_service
      ? 2
      : scope.source === HarnessScopeSource.Any
        ? 0
        : 1;
}
function matches(scope: Document, selected: HarnessScope): boolean {
  return (
    scope.harness === selected.harness &&
    (scope.source === HarnessScopeSource.Any ||
      scope.source === selected.source) &&
    (!scope.provider_id || scope.provider_id === selected.provider_id) &&
    (!scope.subscription_service ||
      scope.subscription_service === selected.subscription_service) &&
    (!scope.api_protocol || scope.api_protocol === selected.api_protocol)
  );
}
export function matchingHarnessSettings(
  defaults: unknown,
  scope: HarnessScope,
): Document[] {
  return items(object(defaults).entries)
    .map(object)
    .filter((entry) => matches(object(entry.scope), scope))
    .sort((a, b) => rank(object(b.scope)) - rank(object(a.scope)))
    .map((entry) => object(entry.settings));
}
export function scopedHarnessSettings(
  defaults: unknown,
  scope: HarnessScope,
): Document {
  const entry = items(object(defaults).entries)
    .map(object)
    .find(
      (entry) =>
        harnessScopeKey(object(entry.scope) as unknown as HarnessScope) ===
        harnessScopeKey(scope),
    );
  return entry ? object(entry.settings) : { version: 1 };
}
// Change only the exact scope. Other harnesses, scope entries and selectors retain
// their original order and values, including explicit empty strings and zero.
export function updateHarnessDefaults(
  defaults: unknown,
  scope: HarnessScope,
  settings: Document,
): Document {
  const entries = [...items(object(defaults).entries)];
  const index = entries.findIndex(
    (entry) =>
      harnessScopeKey(
        object(object(entry).scope) as unknown as HarnessScope,
      ) === harnessScopeKey(scope),
  );
  const original = index < 0 ? {} : object(entries[index]);
  const entry = {
    ...original,
    scope: { ...object(original.scope), ...scope },
    settings,
  };
  if (index < 0) entries.push(entry);
  else entries[index] = entry;
  return { ...object(defaults), version: 1, entries };
}
export interface HarnessSettingsLayer {
  owner: HarnessSettingsOwner;
  settings: Document[];
  revision?: string;
}
export function configuredHarnessValue(
  field: string,
  layers: HarnessSettingsLayer[],
):
  | { value: string | number; owner: HarnessSettingsOwner; revision?: string }
  | undefined {
  for (const layer of layers)
    for (const settings of layer.settings) {
      const selector = object(settings[field]);
      if (
        selector.mode === HarnessSettingMode.Override &&
        (typeof selector.value === "string" ||
          typeof selector.value === "number")
      )
        return {
          value: selector.value,
          owner: layer.owner,
          revision: layer.revision,
        };
    }
  return undefined;
}
export function setHarnessSelector(
  settings: Document,
  field: string,
  mode: HarnessSettingMode,
  value: string | number,
): Document {
  return {
    ...settings,
    version: 1,
    [field]: mode === HarnessSettingMode.Inherit ? { mode } : { mode, value },
  };
}

export interface HarnessRoutePreview {
  label?: string;
  scope: HarnessScope;
  modelSelection: Document;
}
// Routing identity is independent of model inheritance. Never substitute a
// different source to make the preview available. Native admission remains owned
// by the server after it selects the exact account and API profile.
export function harnessRoutePreviews(
  routes: unknown,
  harness: HarnessScope["harness"] = "codex",
): HarnessRoutePreview[] {
  return items(routes)
    .map(object)
    .flatMap((route) => {
      const model = object(route.model);
      const scope: HarnessScope | undefined =
        typeof model.provider_id === "string"
          ? {
              harness,
              source: HarnessScopeSource.Api,
              provider_id: model.provider_id,
            }
          : typeof model.subscription_service === "string"
            ? {
                harness,
                source: HarnessScopeSource.Subscription,
                subscription_service: model.subscription_service,
              }
            : undefined;
      return scope
        ? [{ scope, modelSelection: object(route.model_selection) }]
        : [];
    });
}
