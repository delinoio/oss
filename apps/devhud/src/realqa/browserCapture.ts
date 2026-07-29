import { invoke } from "@tauri-apps/api/core";

export interface BrowserPageMetadata {
  readonly url?: string;
  readonly title?: string;
}

export interface BrowserImage {
  readonly mediaType: "png" | "jpeg";
  readonly base64: string;
  readonly encodedBytes: number;
}

export interface BrowserDomSelection {
  readonly boundary?: {
    readonly x: number;
    readonly y: number;
    readonly width: number;
    readonly height: number;
  };
  readonly selector?: string;
  readonly tag?: string;
  readonly role?: string;
  readonly accessibleName?: string;
  readonly viewport?: {
    readonly width: number;
    readonly height: number;
    readonly devicePixelRatio: number;
  };
}

export interface BrowserCapture {
  readonly kind: "submit-capture";
  readonly version: 1;
  readonly requestId: string;
  readonly captureMode: "visible-viewport" | "os-capture";
  readonly page?: BrowserPageMetadata;
  readonly image?: BrowserImage;
  readonly selection?: BrowserDomSelection;
}

export function takeBrowserCapture(): Promise<BrowserCapture | null> {
  return invoke<BrowserCapture | null>("realqa_take_browser_capture");
}
