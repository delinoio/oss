// Presentation only. The existing controllers retain all lifecycle authority.
export enum ServerPresentationKind { Local = "local", Saved = "saved" }
export type ServerPresentation = { kind: ServerPresentationKind.Local } | { kind: ServerPresentationKind.Saved; name: string };
