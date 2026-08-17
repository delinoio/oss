export type BrowserContextState =
  | { readonly kind: "absent" }
  | { readonly kind: "denied" }
  | { readonly kind: "revoked" }
  | { readonly kind: "disconnected" }
  | { readonly kind: "incognito" }
  | { readonly kind: "malformed" }
  | { readonly kind: "sanitized"; readonly context: SanitizedBrowserContext };

export interface SanitizedBrowserContext {
  readonly url: string;
  readonly title: string;
  readonly viewport: { readonly width: number; readonly height: number };
  readonly userAgent: string;
  readonly selectedBounds: { readonly x: number; readonly y: number; readonly width: number; readonly height: number } | null;
  readonly accessibility: Readonly<Record<string, string>>;
  readonly outerHtml: string;
}

export type BrowserContextSource = "chrome" | "contract-permitted-other";

/** Chrome can never include query or fragment data; other sources need consent. */
export function queryFragmentWarningRequired(source: BrowserContextSource, includeQueryOrFragment: boolean): boolean {
  if (source === "chrome" && includeQueryOrFragment) throw new TypeError("Chrome context cannot include query or fragment data");
  return source === "contract-permitted-other" && includeQueryOrFragment;
}

export function sanitizeChromeContext(input: Omit<SanitizedBrowserContext, "url"> & { readonly url: string }): BrowserContextState {
  try {
    const url = new URL(input.url);
    if (url.protocol !== "http:" && url.protocol !== "https:") throw new Error();
    const path = url.pathname.split("/").map((segment) => segment === "" ? "" : "<redacted>").join("/");
    return { kind: "sanitized", context: { ...input, url: `${url.protocol}//${url.hostname.toLowerCase()}${url.port ? `:${url.port}` : ""}${path}` } };
  } catch { return { kind: "malformed" }; }
}
