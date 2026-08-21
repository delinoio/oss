import { useEffect, useMemo, useState } from "react";
import type { AppProps } from "./App";
import type { Copy } from "./localization";
import { nativeMessaging } from "./native-messaging";
import { useIdentitySettings } from "./service-boundary";

const configurationRetryBaseMilliseconds = 250;
const configurationRetryMaximumMilliseconds = 30_000;

export function SynchronizedNativeMessagingBoundary() {
  const { account, settings, status } = useIdentitySettings();
  const identityScope = account?.userId?.value ?? account?.logtoSubject ?? status;
  const scopeId = useMemo(() => crypto.randomUUID(), [identityScope]);
  useEffect(() => {
    let cancelled = false;
    let retryAttempt = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    const publish = () => {
      void nativeMessaging.configure(settings, scopeId).catch(() => {
        if (cancelled) return;
        const delay = Math.min(configurationRetryMaximumMilliseconds, configurationRetryBaseMilliseconds * (2 ** retryAttempt));
        retryAttempt = Math.min(retryAttempt + 1, 7);
        retryTimer = setTimeout(publish, delay);
      });
    };
    publish();
    return () => {
      cancelled = true;
      if (retryTimer !== undefined) clearTimeout(retryTimer);
    };
  }, [scopeId, settings]);
  return null;
}

export function NativeMessagingSettings({ copy }: { readonly copy: Copy }) {
  const [paired, setPaired] = useState(false);
  const [pairing, setPairing] = useState<{ readonly nonce: string; readonly expiresAt: number } | null>(null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let current = true;
    void nativeMessaging.status().then((status) => { if (current) { setFailed(false); setPaired(status.paired); } }).catch(() => { if (current) setFailed(true); });
    return () => { current = false; };
  }, []);
  useEffect(() => {
    if (pairing === null) return;
    let current = true;
    const poll = setInterval(() => {
      void nativeMessaging.status().then((status) => {
        if (!current) return;
        setFailed(false);
        setPaired(status.paired);
        if (status.paired) setPairing(null);
      }).catch(() => { if (current) setFailed(true); });
    }, 1_000);
    const expiry = setTimeout(() => { if (current) setPairing(null); }, Math.max(0, pairing.expiresAt - Date.now()));
    return () => { current = false; clearInterval(poll); clearTimeout(expiry); };
  }, [pairing]);
  const begin = () => { setFailed(false); void nativeMessaging.beginPairing().then((status) => {
    const lifetime = status.expiresInSeconds;
    setPairing(typeof status.pairingNonce === "string" && status.pairingNonce !== "" && typeof lifetime === "number" && Number.isFinite(lifetime) && lifetime > 0
      ? { nonce: status.pairingNonce, expiresAt: Date.now() + lifetime * 1_000 }
      : null);
    setPaired(false);
  }).catch(() => setFailed(true)); };
  const remove = () => { setFailed(false); void nativeMessaging.unpair().then(() => { setPairing(null); setPaired(false); }).catch(() => setFailed(true)); };
  return <section className="native-setting" aria-labelledby="native-messaging-title">
    <h3 id="native-messaging-title">{copy.nativeMessagingTitle}</h3><p>{copy.nativeMessagingSummary}</p>
    <p className={failed ? "native-setting-error" : undefined} role="status">{failed ? copy.nativeMessagingFailed : paired ? copy.nativeMessagingPaired : copy.nativeMessagingNotPaired}</p>
    {pairing && <p>{copy.nativeMessagingPairingCode}: <code>{pairing.nonce}</code></p>}
    <button className="primary" type="button" onClick={begin}>{copy.nativeMessagingPair}</button>
    {(paired || pairing) && <button type="button" onClick={remove}>{copy.nativeMessagingRemove}</button>}
  </section>;
}

export const desktopNativeMessagingIntegration = {
  Boundary: SynchronizedNativeMessagingBoundary,
  Settings: NativeMessagingSettings,
  takeContext: nativeMessaging.takeContext,
} satisfies NonNullable<AppProps["nativeMessaging"]>;
