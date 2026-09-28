import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { DeviceQuery, EntityKind, ResourceQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, resourceName, text } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export function DeviceDetails({ resource, currentDeviceId }: { resource: Resource; currentDeviceId?: string }) {
  const value = document(resource);
  return <><p>Type: {text(value.type) || "Unknown"} · Authorization: {value.revoked === true ? "Revoked" : value.revoked === false ? "Authorized" : "Unknown"}{resource.id === currentDeviceId ? " · This desktop client" : ""}</p><p>Paired: {text(value.paired_at) || "Unknown"}{value.revoked === true ? ` · Revoked: ${text(value.revoked_at) || "Unknown"}` : ""}</p>{text(value.machine_id) ? <small>Execution machine: {text(value.machine_id)}</small> : null}<p>Authorization does not mean this device is currently connected.</p></>;
}
export function DeviceRevocation({ initial, currentDeviceId, active, close, revoked }: { initial: Resource; currentDeviceId?: string; active: boolean; close: () => void; revoked: () => void }) {
  const [accepted, setAccepted] = useState<Resource | "unknown">();
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.DEVICE, id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const mutation = useRetainedMutation(`revoke-device:${initial.id}`, DeviceQuery.revokeDevice, (result) => { setAccepted(result.device && result.device.id === initial.id && document(result.device).revoked === true ? result.device : "unknown"); revoked(); });
  const stale = Boolean(current.data?.resource && current.data.resource.revision !== initial.revision);
  const unavailable = !current.data?.resource || Boolean(current.error) || initial.kind !== EntityKind.DEVICE || initial.schemaVersion !== 1 || document(initial).revoked !== false;
  const blocked = mutation.busy || mutation.uncertain;
  if (accepted) return <section><h3>Device revocation</h3>{accepted === "unknown" ? <p role="alert">The request was acknowledged without a confirmed device result. Inspect the original device before any further action.</p> : <><p role="status">Authorization revoked for {resourceName(accepted)}.</p><DeviceDetails resource={accepted} currentDeviceId={currentDeviceId} /></>}<p>Retained sessions stay saved. Revocation does not confirm native cleanup or erase the device's private files.</p><button onClick={close}>Return to devices</button></section>;
  return <section><h3>Revoke {resourceName(initial)}</h3><DeviceDetails resource={initial} currentDeviceId={currentDeviceId} /><p>Revoke this device's server authorization and terminate its connection. Active Worker operations may need recovery. This does not remove sessions or register a replacement device.</p>{initial.id === currentDeviceId ? <p className="notice">This is the current desktop's authorization. Revoking it disconnects this app. Its credential will not be automatically replaced; use the server owner to manage access afterward.</p> : null}{stale ? <p role="alert">This device changed after the confirmation opened. Return to the latest device before a new revocation.</p> : null}<Problem error={current.error || mutation.error} /><div className="actions"><button disabled={blocked || stale || unavailable} onClick={() => void mutation.send({ mutation: { id: initial.id, expectedRevision: initial.revision, requestId: newRequestId() } })}>Confirm device revocation</button>{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry original device revocation</button> : null}<button disabled={blocked} onClick={close}>Keep device authorized</button></div></section>;
}

export { Doctor } from "./doctor";
