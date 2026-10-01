import { useId, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { DeviceQuery, EntityKind, ResourceQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";
import "./device-settings.css";

enum DeviceAuthorization { Authorized = "Authorized", Revoked = "Revoked", Unknown = "Unknown" }
export enum DeviceRevocationExit { Cancel, Result }
export enum DeviceAction { Details = "details", Revoke = "revoke" }
const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

export function deviceDate(value: unknown): string {
  const raw = text(value);
  const parts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|[+-](\d{2}):(\d{2}))$/.exec(raw);
  if (!parts) return "Unknown";
  const [, year, month, day, hour, minute, second, , offsetHour, offsetMinute] = parts;
  const y = Number(year), m = Number(month), d = Number(day);
  const monthDays = [31, y % 4 === 0 && (y % 100 !== 0 || y % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  // Date.parse normalizes impossible calendar dates; reject those instead of
  // presenting a fabricated summary. The original text remains in Details.
  if (m < 1 || m > 12 || d < 1 || d > monthDays[m - 1] || Number(hour) > 23 || Number(minute) > 59 || Number(second) > 59 || Number(offsetHour ?? 0) > 23 || Number(offsetMinute ?? 0) > 59) return "Unknown";
  const date = new Date(raw);
  if (!Number.isFinite(date.getTime())) return "Unknown";
  return `${String(date.getUTCDate()).padStart(2, "0")} ${months[date.getUTCMonth()]} ${String(date.getUTCFullYear()).padStart(4, "0")}, ${String(date.getUTCHours()).padStart(2, "0")}:${String(date.getUTCMinutes()).padStart(2, "0")} UTC`;
}

function DeviceMetadata({ resource }: { resource: Resource }) {
  const value = document(resource);
  return <dl className="paired-device-metadata">
    <dt>Device ID</dt><dd>{resource.id}</dd>
    {text(value.machine_id) ? <><dt>Runner Device ID</dt><dd>{text(value.machine_id)}</dd></> : null}
    <dt>Original paired timestamp</dt><dd>{text(value.paired_at) || "Unknown"}</dd>
    {value.revoked === true || text(value.revoked_at) ? <><dt>Original revoked timestamp</dt><dd>{text(value.revoked_at) || "Unknown"}</dd></> : null}
    {text(value.type) && value.type !== "client" && value.type !== "worker" ? <><dt>Type</dt><dd>{text(value.type)}</dd></> : null}
    {text(value.health) ? <><dt>Status</dt><dd>{text(value.health)}</dd></> : null}
    {text(value.harness) ? <><dt>Harness</dt><dd>{text(value.harness)}</dd></> : null}
  </dl>;
}

export function DeviceRow({ resource, currentDeviceId, expanded, toggle, revoke }: { resource: Resource; currentDeviceId?: string; expanded: boolean; toggle: () => void; revoke: () => void }) {
  const value = document(resource), name = text(value.name) || text(value.alias) || "Unknown";
  const authorization = value.revoked === true ? DeviceAuthorization.Revoked : value.revoked === false ? DeviceAuthorization.Authorized : DeviceAuthorization.Unknown;
  const id = useId();
  const self = resource.id === currentDeviceId;
  return <article className="paired-device-row" aria-label={name}>
    <div className="paired-device-main">
      <svg className="paired-device-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={value.type === "worker" ? "M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01" : "M3 4h18v13H3zM8 21h8m-4-4v4"} /></svg>
      <div className="paired-device-identity"><h3>{name}</h3><div className="paired-device-facts"><span>{value.type === "client" ? "Desktop client" : value.type === "worker" ? "Worker" : "Unknown"}</span><span className={authorization === DeviceAuthorization.Authorized ? "paired-device-badge paired-device-authorized" : "paired-device-badge"}>{authorization}</span>{self ? <span>This desktop client</span> : null}</div><p className="paired-device-dates">Paired: {deviceDate(value.paired_at)}{value.revoked === true ? ` · Revoked: ${deviceDate(value.revoked_at)}` : ""}</p>{self ? <p className="paired-device-self">This desktop client cannot revoke its own registration.</p> : null}</div>
      <div className="actions paired-device-actions"><button type="button" data-device-id={resource.id} data-device-action={DeviceAction.Details} aria-label={`Details for ${name}`} aria-expanded={expanded} aria-controls={id} onClick={toggle}>Details<span aria-hidden="true"> {expanded ? "⌃" : "⌄"}</span></button>{value.revoked === false && !self ? <button type="button" className="paired-device-revoke" data-device-id={resource.id} data-device-action={DeviceAction.Revoke} disabled={resource.schemaVersion !== 1} aria-label={`Revoke ${name}`} onClick={revoke}>Revoke</button> : null}</div>
    </div>
    <div id={id} hidden={!expanded} className="paired-device-disclosure"><DeviceMetadata resource={resource} /></div>
  </article>;
}

export function DeviceDetails({ resource, currentDeviceId }: { resource: Resource; currentDeviceId?: string }) {
  const value = document(resource);
  return <><p>Type: {value.type === "client" ? "Desktop client" : value.type === "worker" ? "Worker" : "Unknown"} · Authorization: {value.revoked === true ? "Revoked" : value.revoked === false ? "Authorized" : "Unknown"}{resource.id === currentDeviceId ? " · This desktop client" : ""}</p><DeviceMetadata resource={resource} /><p>Authorization does not mean this device is currently connected.</p></>;
}
export function DeviceRevocation({ initial, currentDeviceId, active, close, revoked }: { initial: Resource; currentDeviceId?: string; active: boolean; close: (exit: DeviceRevocationExit) => void; revoked: () => void }) {
  const [accepted, setAccepted] = useState<Resource | "unknown">();
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.DEVICE, id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const mutation = useRetainedMutation(`revoke-device:${initial.id}`, DeviceQuery.revokeDevice, (result) => { setAccepted(result.device && result.device.id === initial.id && document(result.device).revoked === true ? result.device : "unknown"); revoked(); });
  const stale = Boolean(current.data?.resource && current.data.resource.revision !== initial.revision);
  const unavailable = !current.data?.resource || Boolean(current.error) || initial.kind !== EntityKind.DEVICE || initial.schemaVersion !== 1 || document(initial).revoked !== false;
  const self = initial.id === currentDeviceId;
  const blocked = mutation.busy || mutation.uncertain;
  if (accepted) return <section className="paired-device-form"><h3>Device revocation</h3>{accepted === "unknown" ? <p role="alert">The request was acknowledged without a confirmed device result. Inspect the original device before any further action.</p> : <><p role="status">Authorization revoked for {resourceName(accepted)}.</p><DeviceDetails resource={accepted} currentDeviceId={currentDeviceId} /></>}<p>Retained sessions stay saved. Revocation does not confirm native cleanup or erase the device's private files.</p><button onClick={() => close(DeviceRevocationExit.Result)}>Return to devices</button></section>;
  return <section className="paired-device-form"><h3>Revoke {resourceName(initial)}</h3><DeviceDetails resource={initial} currentDeviceId={currentDeviceId} /><p>Revoke this device's server authorization and terminate its connection. Active Worker operations may need recovery. This does not remove sessions or register a replacement device.</p>{initial.id === currentDeviceId ? <p className="notice">This is the current desktop's authorization. It cannot be revoked from this app. Use another authorized client or the server owner to manage its access.</p> : null}{stale ? <p role="alert">This device changed after the confirmation opened. Return to the latest device before a new revocation.</p> : null}<Problem error={current.error || mutation.error} /><div className="actions"><button className="paired-device-revoke" disabled={self || blocked || stale || unavailable} onClick={() => { if (self || blocked || stale || unavailable) return; void mutation.send({ mutation: { id: initial.id, expectedRevision: initial.revision, requestId: newRequestId() } }); }}>Confirm device revocation</button>{mutation.uncertain && !self ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry original device revocation</button> : null}<button disabled={blocked} onClick={() => close(DeviceRevocationExit.Cancel)}>Keep device authorized</button></div></section>;
}

export { Doctor } from "./doctor";
