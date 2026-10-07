// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { copy, useLocale } from "./localization";

const Presentation = createContext<{ target?: HTMLElement; inline: boolean; requestInline?: () => void }>({ inline: true });
export function LocalConnectionPresentationProvider({ target, inline, children, onRequest }: { onRequest?: () => void; target?: HTMLElement; inline: boolean; children: ReactNode }) {
  const [requested, setRequested] = useState(false);
  return <Presentation.Provider value={{ target, inline: inline || requested, requestInline: () => { setRequested(true); onRequest?.(); } }}>{children}</Presentation.Provider>;
}
// Move only the view. The original controller stays mounted in its authenticated
// mutation scope when diagnostics opens or closes, retaining confirmations.
export function LocalConnectionPresentation({ children, diagnosticsOnly = false }: { children: ReactNode; diagnosticsOnly?: boolean }) {
  const { target, inline } = useContext(Presentation);
  return target ? createPortal(children, target) : <div hidden={!inline || diagnosticsOnly}>{children}</div>;
}

// Opening a local view neither probes credentials nor repairs the connection.
export function LocalConnectionHelp() {
  useLocale();
  const { requestInline } = useContext(Presentation);
  return requestInline ? <button type="button" onClick={requestInline}>{copy("desktop.connectionControls_6f99ea")}</button> : null;
}
