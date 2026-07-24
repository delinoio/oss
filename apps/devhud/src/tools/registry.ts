import type { ComponentType } from "react";

export enum ToolPlatform {
  Desktop = "desktop",
  Ios = "ios",
  Android = "android",
}

export enum ToolCapability {
  Diagnostics = "diagnostics",
  LocalStorage = "local-storage",
  WindowControl = "window-control",
}

export interface ToolDefinition {
  readonly toolId: string;
  readonly name: string;
  readonly description: string;
  readonly searchKeywords: readonly string[];
  readonly supportedPlatforms: ReadonlySet<ToolPlatform>;
  readonly requiredCapabilities: ReadonlySet<ToolCapability>;
  readonly EntryPoint: ComponentType;
}

export interface ToolContext {
  readonly platform: ToolPlatform;
  readonly grantedCapabilities: ReadonlySet<ToolCapability>;
}

const TOOL_ID = /^[a-z]+(?:-[a-z0-9]+)*$/u;
const ENGLISH_TEXT = /^[\x20-\x7E]+$/u;
const PLATFORMS = new Set(Object.values(ToolPlatform));
const CAPABILITIES = new Set(Object.values(ToolCapability));

export function defineTool(definition: ToolDefinition): ToolDefinition {
  if (!TOOL_ID.test(definition.toolId)) {
    throw new Error("Tool IDs must be lowercase kebab-case.");
  }
  if (!ENGLISH_TEXT.test(definition.name) || !ENGLISH_TEXT.test(definition.description)) {
    throw new Error("Tool names and descriptions must be English ASCII text.");
  }
  if (definition.searchKeywords.length === 0 || definition.searchKeywords.some((keyword) => !ENGLISH_TEXT.test(keyword))) {
    throw new Error("Tool search keywords must contain English ASCII text.");
  }
  if (definition.supportedPlatforms.size === 0 || [...definition.supportedPlatforms].some((platform) => !PLATFORMS.has(platform))) {
    throw new Error("Tools must support one or more known platforms.");
  }
  if ([...definition.requiredCapabilities].some((capability) => !CAPABILITIES.has(capability))) {
    throw new Error("Tools must request known capabilities only.");
  }
  return definition;
}

export function filterTools(
  definitions: readonly ToolDefinition[],
  context: ToolContext,
): ToolDefinition[] {
  return definitions.filter(
    (tool) =>
      tool.supportedPlatforms.has(context.platform) &&
      [...tool.requiredCapabilities].every((capability) =>
        context.grantedCapabilities.has(capability),
      ),
  );
}

/** Production registration remains deliberately empty in the foundation preview. */
export const productionTools: readonly ToolDefinition[] = [];
