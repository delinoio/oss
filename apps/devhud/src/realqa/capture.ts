import { invoke } from "@tauri-apps/api/core";

export enum CapturePlatform {
  Macos = "macos",
  Windows = "windows",
  Linux = "linux",
}

export enum CaptureMode {
  Region = "region",
  Window = "window",
  Display = "display",
  MultiMonitor = "multi-monitor",
}

export enum CaptureDisplayProtocol {
  Native = "native",
  X11 = "x11",
  XWayland = "xwayland",
  WaylandPortal = "wayland-portal",
}

export enum SelectionAdjustmentAuthority {
  Application = "application",
  Portal = "portal",
  Unavailable = "unavailable",
}

export enum PointerInclusion {
  Include = "include",
  Exclude = "exclude",
}

export enum CapturePermission {
  Granted = "granted",
  PromptRequired = "prompt-required",
  Denied = "denied",
}

export enum ImageMediaType {
  Png = "png",
  Webp = "webp",
}

export enum CaptureFailure {
  UnsupportedPlatform = "unsupported-platform",
  BackendUnavailable = "backend-unavailable",
  PermissionRequired = "permission-required",
  PermissionDenied = "permission-denied",
  PermissionLost = "permission-lost",
  Cancelled = "cancelled",
  PortalCancelled = "portal-cancelled",
  ProtectedContent = "protected-content",
  WindowLost = "window-lost",
  ModeUnavailable = "mode-unavailable",
  DisplaySnapshotChanged = "display-snapshot-changed",
  InvalidDisplaySnapshot = "invalid-display-snapshot",
  InvalidSelection = "invalid-selection",
  MalformedImage = "malformed-image",
  UnsupportedImage = "unsupported-image",
  DecompressionBomb = "decompression-bomb",
  ImageEncodedLimitExceeded = "image-encoded-limit-exceeded",
  SessionEncodedLimitExceeded = "session-encoded-limit-exceeded",
  EncodingFailed = "encoding-failed",
  CaptureFailed = "capture-failed",
}

export interface LogicalRect {
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

export interface PhysicalSize {
  readonly width: number;
  readonly height: number;
}

export interface ScaleFactor {
  readonly numerator: number;
  readonly denominator: number;
}

export interface DisplayDescriptor {
  readonly id: string;
  readonly logicalBounds: LogicalRect;
  readonly physicalSize: PhysicalSize;
  readonly scale: ScaleFactor;
  readonly primary: boolean;
}

export interface DisplaySnapshot {
  readonly snapshotId: string;
  readonly displays: readonly DisplayDescriptor[];
}

export enum WindowAvailability {
  Available = "available",
  Minimized = "minimized",
}

export interface WindowSource {
  readonly id: string;
  readonly displayId: string;
  readonly bounds: LogicalRect;
  readonly availability: WindowAvailability;
  readonly metadata: {
    readonly processName: string | null;
    readonly title: string | null;
  };
}

export interface CaptureModeCapability {
  readonly mode: CaptureMode;
  readonly pointerOptions: readonly PointerInclusion[];
  readonly portalApprovalRequired: boolean;
  readonly selectionAdjustment: SelectionAdjustmentAuthority;
}

export interface CaptureCapabilities {
  readonly platform: CapturePlatform;
  readonly displayProtocol: CaptureDisplayProtocol;
  readonly modes: readonly CaptureModeCapability[];
}

export interface CaptureSourceCatalog {
  readonly platform: CapturePlatform;
  readonly permission: CapturePermission;
  readonly capabilities: CaptureCapabilities;
  readonly snapshot: DisplaySnapshot;
  readonly windows: readonly WindowSource[];
}

export interface SelectionGeometry {
  readonly snapshotId: string;
  readonly bounds: LogicalRect;
}

export enum ResizeHandle {
  North = "north",
  NorthEast = "north-east",
  East = "east",
  SouthEast = "south-east",
  South = "south",
  SouthWest = "south-west",
  West = "west",
  NorthWest = "north-west",
}

export type SelectionAdjustment =
  | {
      readonly kind: "move";
      readonly deltaX: number;
      readonly deltaY: number;
    }
  | {
      readonly kind: "resize";
      readonly handle: ResizeHandle;
      readonly deltaX: number;
      readonly deltaY: number;
    };

export type CaptureSourceSelection =
  | { readonly mode: CaptureMode.Region; readonly selection: SelectionGeometry }
  | { readonly mode: CaptureMode.Window; readonly windowId: string }
  | { readonly mode: CaptureMode.Display; readonly displayId: string }
  | {
      readonly mode: CaptureMode.MultiMonitor;
      readonly displayIds: readonly string[];
    };

export interface EncodedImage {
  readonly mediaType: ImageMediaType;
  readonly bytes: readonly number[];
}

export interface CaptureRequest {
  readonly sessionId: string;
  readonly snapshotId: string;
  readonly source: CaptureSourceSelection;
  readonly pointer: PointerInclusion;
  readonly outputMediaType: ImageMediaType;
}

export interface PixelRect {
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

export interface DisplayPixelRegion {
  readonly displayId: string;
  readonly pixels: PixelRect;
}

export interface CaptureResult {
  readonly mode: CaptureMode;
  readonly pointer: PointerInclusion;
  readonly logicalBounds: LogicalRect;
  readonly pixelRegions: readonly DisplayPixelRegion[];
  readonly image: EncodedImage;
}

export interface ComposerImageRequest {
  readonly sessionId: string;
  readonly imageId: string;
  readonly image: EncodedImage;
  readonly outputMediaType: ImageMediaType;
}

export interface ComposerImage {
  readonly imageId: string;
  readonly contentType: "image/png" | "image/webp";
  readonly width: number;
  readonly height: number;
  readonly encodedBytes: number;
  readonly sessionEncodedBytes: number;
  readonly image: EncodedImage;
}

export type InvokeCommand = <T>(
  command: string,
  arguments_?: Record<string, unknown>,
) => Promise<T>;

export interface RealQaCaptureBridge {
  inspectCapabilities(): Promise<CaptureCapabilities>;
  listSources(): Promise<CaptureSourceCatalog>;
  adjustSelection(
    selection: SelectionGeometry,
    adjustment: SelectionAdjustment,
  ): Promise<SelectionGeometry>;
  beginCapture(request: CaptureRequest): Promise<CaptureResult>;
  cancelCapture(sessionId: string): Promise<void>;
}

export interface RealQaComposerBridge {
  acceptImage(request: ComposerImageRequest): Promise<ComposerImage>;
  removeImage(sessionId: string, imageId: string): Promise<void>;
  resetSession(sessionId: string): Promise<void>;
}

export function createRealQaCaptureBridge(
  invokeCommand: InvokeCommand = invoke,
): RealQaCaptureBridge {
  return {
    inspectCapabilities: () =>
      invokeCommand<CaptureCapabilities>(
        "realqa_inspect_capture_capabilities",
      ),
    listSources: () =>
      invokeCommand<CaptureSourceCatalog>("realqa_list_capture_sources"),
    adjustSelection: (selection, adjustment) =>
      invokeCommand<SelectionGeometry>("realqa_adjust_capture_selection", {
        selection,
        adjustment,
      }),
    beginCapture: (request) =>
      invokeCommand<CaptureResult>("realqa_begin_capture", { request }),
    cancelCapture: (sessionId) =>
      invokeCommand<void>("realqa_cancel_capture", { sessionId }),
  };
}

export function createRealQaComposerBridge(
  invokeCommand: InvokeCommand = invoke,
): RealQaComposerBridge {
  return {
    acceptImage: (request) =>
      invokeCommand<ComposerImage>("realqa_composer_accept_image", { request }),
    removeImage: (sessionId, imageId) =>
      invokeCommand<void>("realqa_composer_remove_image", {
        sessionId,
        imageId,
      }),
    resetSession: (sessionId) =>
      invokeCommand<void>("realqa_composer_reset_session", { sessionId }),
  };
}
