import { useEffect, useState } from "react";
import type { Copy } from "./localization.ts";
import { NativeBridgeError, NativeBridgeErrorCode, SecureSettingKind, type NativeBridgeV1 } from "./native-bridge.ts";
import { useIdentitySettings } from "./service-boundary.tsx";
import { parseDevHudSettings } from "./settings-contract.ts";

export function R2Settings({ copy, bridge }: { readonly copy: Copy; readonly bridge: NativeBridgeV1 }) {
  const identity = useIdentitySettings();
  const configured = identity.settings.uploads.r2;
  const [name, setName] = useState(configured?.name ?? "R2");
  const [endpoint, setEndpoint] = useState(configured?.endpoint ?? "");
  const [accountId, setAccountId] = useState(configured?.accountId ?? "");
  const [bucket, setBucket] = useState(configured?.bucket ?? "");
  const [publicBaseUrl, setPublicBaseUrl] = useState(configured?.publicBaseUrl ?? "");
  const [prefix, setPrefix] = useState(configured?.prefix ?? "devhud");
  const [accessKeyId, setAccessKeyId] = useState("");
  const [secretAccessKey, setSecretAccessKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    setName(configured?.name ?? "R2"); setEndpoint(configured?.endpoint ?? ""); setAccountId(configured?.accountId ?? ""); setBucket(configured?.bucket ?? ""); setPublicBaseUrl(configured?.publicBaseUrl ?? ""); setPrefix(configured?.prefix ?? "devhud");
  }, [configured]);

  const save = async () => {
    if (busy) return;
    setBusy(true); setFailed(false); setMessage("");
    const profileRef = configured?.profileRef ?? uuidV7();
    const settingId = { kind: SecureSettingKind.R2AccessKeyId, profileId: profileRef } as const;
    const settingSecret = { kind: SecureSettingKind.R2SecretAccessKey, profileId: profileRef } as const;
    try {
      const next = parseDevHudSettings({ ...identity.settings, uploads: { provider: "r2", r2: { profileRef, name, endpoint, accountId, bucket, publicBaseUrl, prefix } } });
      const [oldId, oldSecret] = await Promise.all([bridge.request({ operation: "secure.read", setting: settingId }), bridge.request({ operation: "secure.read", setting: settingSecret })]);
      if ((accessKeyId === "" && oldId.kind === "secure-value" && oldId.value === null) || (secretAccessKey === "" && oldSecret.kind === "secure-value" && oldSecret.value === null)) throw new NativeBridgeError(NativeBridgeErrorCode.NotConfigured);
      const writes: Promise<unknown>[] = [];
      if (accessKeyId !== "") writes.push(bridge.request({ operation: "secure.write", setting: settingId, value: accessKeyId }));
      if (secretAccessKey !== "") writes.push(bridge.request({ operation: "secure.write", setting: settingSecret, value: secretAccessKey }));
      try {
        await Promise.all(writes);
        if (!await identity.replaceSettings(next)) throw new Error("settings-write-failed");
      } catch (reason) {
        await Promise.allSettled([
          restore(bridge, settingId, oldId.kind === "secure-value" ? oldId.value : null),
          restore(bridge, settingSecret, oldSecret.kind === "secure-value" ? oldSecret.value : null),
        ]);
        throw reason;
      }
      setAccessKeyId(""); setSecretAccessKey(""); setMessage(copy.r2Saved);
    } catch { setFailed(true); setMessage(copy.r2SaveFailed); }
    finally { setBusy(false); }
  };

  const useOfficial = async () => {
    setBusy(true); setFailed(false);
    try {
      if (!await identity.replaceSettings((current) => ({ ...current, uploads: { ...current.uploads, provider: "official" } }))) throw new Error();
      setMessage(copy.r2OfficialSelected);
    } catch { setFailed(true); setMessage(copy.r2SaveFailed); }
    finally { setBusy(false); }
  };

  return <section className="native-setting" aria-labelledby="r2-settings-title"><h3 id="r2-settings-title">{copy.r2SettingsTitle}</h3><p>{copy.r2SettingsSummary}</p>
    <label>{copy.r2ProfileName}<input value={name} disabled={busy || identity.readOnly} onChange={(event) => setName(event.target.value)} /></label>
    <label>{copy.r2Endpoint}<input type="url" value={endpoint} disabled={busy || identity.readOnly} onChange={(event) => setEndpoint(event.target.value)} /></label>
    <label>{copy.r2AccountId}<input value={accountId} disabled={busy || identity.readOnly} onChange={(event) => setAccountId(event.target.value)} /></label>
    <label>{copy.r2Bucket}<input value={bucket} disabled={busy || identity.readOnly} onChange={(event) => setBucket(event.target.value)} /></label>
    <label>{copy.r2PublicBase}<input type="url" value={publicBaseUrl} disabled={busy || identity.readOnly} onChange={(event) => setPublicBaseUrl(event.target.value)} /></label>
    <label>{copy.r2Prefix}<input value={prefix} disabled={busy || identity.readOnly} onChange={(event) => setPrefix(event.target.value)} /></label>
    <label>{copy.r2AccessKeyId}<input autoComplete="off" value={accessKeyId} disabled={busy || identity.readOnly} onChange={(event) => setAccessKeyId(event.target.value)} /></label>
    <label>{copy.r2SecretAccessKey}<input type="password" autoComplete="new-password" value={secretAccessKey} disabled={busy || identity.readOnly} onChange={(event) => setSecretAccessKey(event.target.value)} /></label>
    <p className="notice">{copy.issuePublicImageWarning}</p>
    <div className="actions"><button className="primary" disabled={busy || identity.readOnly} onClick={() => void save()}>{copy.r2Save}</button><button disabled={busy || identity.readOnly || identity.settings.uploads.provider === "official"} onClick={() => void useOfficial()}>{copy.r2UseOfficial}</button></div>
    {message && <p role={failed ? "alert" : "status"}>{message}</p>}
  </section>;
}

async function restore(bridge: NativeBridgeV1, setting: { readonly kind: typeof SecureSettingKind.R2AccessKeyId | typeof SecureSettingKind.R2SecretAccessKey; readonly profileId: string }, value: string | null) {
  await bridge.request(value === null ? { operation: "secure.remove", setting } : { operation: "secure.write", setting, value });
}

function uuidV7(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  const timestamp = BigInt(Date.now());
  for (let index = 0; index < 6; index += 1) bytes[5 - index] = Number(timestamp >> BigInt(index * 8) & 0xffn);
  bytes[6] = 0x70 | bytes[6] & 0x0f; bytes[8] = 0x80 | bytes[8] & 0x3f;
  const hex = [...bytes].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
