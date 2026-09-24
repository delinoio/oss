export { createSession, importOffice, DocumentSession } from "./session.js";
export type { Inspection, TargetHandle, MountedRegion } from "./session.js";
export { ForgeError } from "./errors.js";
export { Format, ErrorCode, Stage, limits } from "./types.js";
export type { NodeHandle, Geometry, Diagnostic, AssetSource, AssetHandle, TextStyle, Frame } from "./types.js";
export { capabilities } from "./capabilities.js";
export type { TaskContext as McpTaskContext, SessionTask as McpSessionTask } from "./mcp/runtime.js";

export { FigmaSession, openFigma, PublishStatus, FigmaPublishError } from "./figma/session.js";
export { CredentialSource } from "./figma/credentials.js";
export type { FigmaOptions, FigmaReceipt, FigmaTarget } from "./figma/session.js";
