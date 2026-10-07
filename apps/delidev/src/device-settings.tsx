import { statusLabel } from "./product-status";
import { formatTimestamp } from "./localization";
import { LocalizedText, copy, displayLocale, useLocale } from "./localization";
import { SettingsTaskActions } from "./settings-task";
import { useCloseSettingsTask } from "./settings-task-context";
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

export function deviceDate(value: unknown): string {
  const raw = text(value);
  const parts = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|[+-](\d{2}):(\d{2}))$/.exec(raw);
  if (!parts) return copy("device-settings.extra.b764cdc0eab7");
  const [, year, month, day, hour, minute, second, , offsetHour, offsetMinute] = parts;
  const y = Number(year), m = Number(month), d = Number(day);
  const monthDays = [31, y % 4 === 0 && (y % 100 !== 0 || y % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  // Date.parse normalizes impossible calendar dates; reject those instead of
  // presenting a fabricated summary. The original text remains in Details.
  if (m < 1 || m > 12 || d < 1 || d > monthDays[m - 1] || Number(hour) > 23 || Number(minute) > 59 || Number(second) > 59 || Number(offsetHour ?? 0) > 23 || Number(offsetMinute ?? 0) > 59) return copy("device-settings.extra.b764cdc0eab7");
  const date = new Date(raw);
  if (!Number.isFinite(date.getTime())) return copy("device-settings.extra.b764cdc0eab7");
  const formatter = new Intl.DateTimeFormat(displayLocale(), { timeZone: "UTC", year: "numeric", month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZoneName: "short" });
  if (displayLocale() === "en-US") {
    const parts = new Map(formatter.formatToParts(date).map(part => [part.type, part.value]));
    return `${parts.get("day")} ${parts.get("month")} ${parts.get("year")}, ${parts.get("hour")}:${parts.get("minute")} UTC`;
  }
  return formatter.format(date);
}

function DeviceMetadata({ resource }: { resource: Resource }) {
  useLocale();
  const value = document(resource);
  return <dl className="paired-device-metadata">
    <dt>{copy("device-settings.deviceId_6e0a0b")}</dt><dd>{resource.id}</dd>
    {text(value.machine_id) ? <><dt>{copy("device-settings.runnerDeviceId_a03c92")}</dt><dd>{text(value.machine_id)}</dd></> : null}
    <dt>{copy("device-settings.originalPairedTimestamp_7fae2b")}</dt><dd>{formatTimestamp(text(value.paired_at)) || copy("device-settings.extra.b764cdc0eab7")}</dd>
    {value.revoked === true || text(value.revoked_at) ? <><dt>{copy("device-settings.originalRevokedTimestamp_c0f6f5")}</dt><dd>{formatTimestamp(text(value.revoked_at)) || copy("device-settings.extra.b764cdc0eab7")}</dd></> : null}
    {text(value.type) && value.type !== "client" && value.type !== "worker" ? <><dt>{copy("device-settings.type_baaddf")}</dt><dd>{text(value.type)}</dd></> : null}
    {text(value.health) ? <><dt>{copy("device-settings.status_920e41")}</dt><dd>{statusLabel(text(value.health))}</dd></> : null}
    {text(value.harness) ? <><dt>{copy("device-settings.harness_e3b5b4")}</dt><dd>{text(value.harness)}</dd></> : null}
  </dl>;
}

export function DeviceRow({ resource, currentDeviceId, expanded, toggle, revoke }: { resource: Resource; currentDeviceId?: string; expanded: boolean; toggle: () => void; revoke: () => void }) {
  useLocale();
  const value = document(resource), name = text(value.name) || text(value.alias) || copy("device-settings.extra.b764cdc0eab7");
  const authorization = value.revoked === true ? DeviceAuthorization.Revoked : value.revoked === false ? DeviceAuthorization.Authorized : DeviceAuthorization.Unknown;
  const id = useId();
  const self = resource.id === currentDeviceId;
  return <article className="paired-device-row" aria-label={name}>
    <div className="paired-device-main">
      <svg className="paired-device-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={value.type === "worker" ? "M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01" : "M3 4h18v13H3zM8 21h8m-4-4v4"} /></svg>
      <div className="paired-device-identity"><h3>{name}</h3><div className="paired-device-facts"><span>{value.type === "client" ? copy("device-settings.desktopClient_cfe9ad") : value.type === "worker" ? copy("device-settings.worker_a67b04") : copy("device-settings.unknown_b764cd")}</span><span className={authorization === DeviceAuthorization.Authorized ? "paired-device-badge paired-device-authorized" : "paired-device-badge"}>{authorization === DeviceAuthorization.Authorized ? copy("device-settings.authorized_67f542") : authorization === DeviceAuthorization.Revoked ? copy("device-settings.revoked_f6f738") : copy("device-settings.unknown_b764cd")}</span>{self ? <span>{copy("device-settings.thisDesktopClient_eaab20")}</span> : null}</div><p className="paired-device-dates"><LocalizedText id="device-settings.paired_446e5e" components={{ s0: <>{deviceDate(value.paired_at)}</>, s1: <>{value.revoked === true ? copy("device-settings.revoked_513da5", { v0: deviceDate(value.revoked_at) }) : ""}</> }} /></p>{self ? <p className="paired-device-self">{copy("device-settings.thisDesktopClientCannotRevokeIts_f0f65f")}</p> : null}</div>
      <div className="actions paired-device-actions"><button type="button" data-device-id={resource.id} data-device-action={DeviceAction.Details} aria-label={copy("device-settings.detailsFor_0bbada", { v0: name })} aria-expanded={expanded} aria-controls={id} onClick={toggle}><LocalizedText id="device-settings.details_6bb37e" components={{ s0: <span aria-hidden="true"> {expanded ? "⌃" : "⌄"}</span> }} /></button>{value.revoked === false && !self ? <button type="button" className="paired-device-revoke" data-device-id={resource.id} data-device-action={DeviceAction.Revoke} disabled={resource.schemaVersion !== 1} aria-label={copy("device-settings.revoke_d6e405", { v0: name })} onClick={revoke}>{copy("device-settings.revoke_87e6d0")}</button> : null}</div>
    </div>
    <div id={id} hidden={!expanded} className="paired-device-disclosure"><DeviceMetadata resource={resource} /></div>
  </article>;
}

export function DeviceDetails({ resource, currentDeviceId }: { resource: Resource; currentDeviceId?: string }) {
  useLocale();
  const value = document(resource);
  return <><p><LocalizedText id="device-settings.typeAuthorization_2417b0" components={{ s0: <>{value.type === "client" ? copy("device-settings.desktopClient_cfe9ad") : value.type === "worker" ? copy("device-settings.worker_a67b04") : copy("device-settings.unknown_b764cd")}</>, s1: <>{value.revoked === true ? copy("device-settings.revoked_f6f738") : value.revoked === false ? copy("device-settings.authorized_67f542") : copy("device-settings.unknown_b764cd")}</>, s2: <>{resource.id === currentDeviceId ? copy("device-settings.thisDesktopClient_480e33") : ""}</> }} /></p><DeviceMetadata resource={resource} /><p>{copy("device-settings.authorizationDoesNotMeanThisDevice_cfecd2")}</p></>;
}
export function DeviceRevocation({ initial, currentDeviceId, active, close, revoked }: { initial: Resource; currentDeviceId?: string; active: boolean; close: (exit: DeviceRevocationExit) => void; revoked: () => void }) {
  useLocale();
  const [accepted, setAccepted] = useState<Resource | "unknown">();
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.DEVICE, id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const mutation = useRetainedMutation(`revoke-device:${initial.id}`, DeviceQuery.revokeDevice, (result) => { setAccepted(result.device && result.device.id === initial.id && document(result.device).revoked === true ? result.device : "unknown"); revoked(); });
  const stale = Boolean(current.data?.resource && current.data.resource.revision !== initial.revision);
  const unavailable = !current.data?.resource || Boolean(current.error) || initial.kind !== EntityKind.DEVICE || initial.schemaVersion !== 1 || document(initial).revoked !== false;
  const closeTask = useCloseSettingsTask(() => close(DeviceRevocationExit.Result));
  const self = initial.id === currentDeviceId;
  const blocked = mutation.busy || mutation.uncertain;
  if (accepted) return <section className="paired-device-form"><h3>{copy("device-settings.deviceRevocation_ca1002")}</h3>{accepted === "unknown" ? <p role="alert">{copy("device-settings.theRequestWasAcknowledgedWithoutA_5bdbb6")}</p> : <><p role="status"><LocalizedText id="device-settings.authorizationRevokedFor_57a733" components={{ s0: <>{resourceName(accepted)}</> }} /></p><DeviceDetails resource={accepted} currentDeviceId={currentDeviceId} /></>}<p>{copy("device-settings.retainedSessionsStaySavedRevocationDoes_16730e")}</p><SettingsTaskActions><button onClick={() => accepted === "unknown" ? closeTask() : close(DeviceRevocationExit.Result)}>{copy("device-settings.returnToDevices_12e1ef")}</button></SettingsTaskActions></section>;
  return <section className="paired-device-form"><h3><LocalizedText id="device-settings.revoke_eeebdc" components={{ s0: <>{resourceName(initial)}</> }} /></h3><DeviceDetails resource={initial} currentDeviceId={currentDeviceId} /><p>{copy("device-settings.revokeThisDeviceSServerAuthorization_b42692")}</p>{initial.id === currentDeviceId ? <p className="notice">{copy("device-settings.thisIsTheCurrentDesktopS_74900b")}</p> : null}{stale ? <p role="alert">{copy("device-settings.thisDeviceChangedAfterTheConfirmation_174a53")}</p> : null}<Problem error={current.error || mutation.error} actions={current.error ? <button type="button" disabled={!active || blocked || current.isFetching} onClick={() => void current.refetch()}>{copy("ui.retryCurrentRead")}</button> : undefined} /><SettingsTaskActions className=""><button className="paired-device-revoke" disabled={self || blocked || stale || unavailable} onClick={() => { if (self || blocked || stale || unavailable) return; void mutation.send({ mutation: { id: initial.id, expectedRevision: initial.revision, requestId: newRequestId() } }); }}>{copy("device-settings.confirmDeviceRevocation_8cf482")}</button>{mutation.uncertain && !self ? <button disabled={mutation.busy} onClick={mutation.retry}>{copy("device-settings.retryOriginalDeviceRevocation_976bec")}</button> : null}<button data-settings-task-cancel disabled={blocked} onClick={() => close(DeviceRevocationExit.Cancel)}>{copy("device-settings.keepDeviceAuthorized_704f74")}</button></SettingsTaskActions></section>;
}

export { Doctor } from "./doctor";
