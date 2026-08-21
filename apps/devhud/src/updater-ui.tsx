import { type KeyboardEvent, useEffect, useRef, useState } from "react";

import type { SupportedLanguage } from "./localization";
import { NativeBridgeError, NativeBridgeErrorCode, type DesktopUpdaterStatus, type NativeBridgeResponseV1, type NativeBridgeV1 } from "./native-bridge";

const updaterCopy = {
  en: {
    title: "Desktop updates", summary: "DevHUD checks 30 seconds after startup and every 24 active hours. Checks never download or install automatically.",
    installed: "Installed version", running: "Running version", check: "Check for updates", checking: "Checking for a signed update…", current: "DevHUD is up to date.",
    available: "Signed update available", releaseNotes: "Release notes", download: "Approve download", downloading: "Downloading and verifying…", cancel: "Cancel",
    downloaded: "The update is downloaded and verified.", approveInstall: "Approve installation", prepared: "Installation is approved. DevHUD will not change files or restart until you approve the final step.",
    restart: "Install and restart", retryRestart: "Retry restart", recoverRestart: "Restart DevHUD", restartRequired: "The update is installed, but DevHUD is still running the previous version. Retry the restart to finish.", installationUncertain: "The package installer reported a failure after files may have changed. Restart DevHUD without reinstalling to recover.", confirmDownload: "Download this signed update?", confirmInstall: "Approve this verified update for installation?", confirmRestart: "Install the verified update and restart DevHUD now?", confirmRetryRestart: "Restart DevHUD again without reinstalling the update?", confirmRecoveryRestart: "Restart DevHUD without reinstalling the update?",
    confirm: "Confirm", close: "Go back", failed: "The update did not complete. Your installed version was preserved.", canceled: "The update was canceled. Your installed version was preserved.",
  },
  ko: {
    title: "데스크톱 업데이트", summary: "DevHUD는 시작 30초 후와 활성 실행 시간 24시간마다 확인합니다. 자동으로 다운로드하거나 설치하지 않습니다.",
    installed: "설치된 버전", running: "실행 중인 버전", check: "업데이트 확인", checking: "서명된 업데이트를 확인하는 중…", current: "DevHUD가 최신 버전입니다.",
    available: "서명된 업데이트 사용 가능", releaseNotes: "릴리스 노트", download: "다운로드 승인", downloading: "다운로드 및 검증 중…", cancel: "취소",
    downloaded: "업데이트를 다운로드하고 검증했습니다.", approveInstall: "설치 승인", prepared: "설치가 승인되었습니다. 마지막 단계를 승인하기 전에는 파일을 변경하거나 다시 시작하지 않습니다.",
    restart: "설치 후 다시 시작", retryRestart: "다시 시작 재시도", recoverRestart: "DevHUD 다시 시작", restartRequired: "업데이트가 설치되었지만 DevHUD는 아직 이전 버전으로 실행 중입니다. 완료하려면 다시 시작을 재시도하세요.", installationUncertain: "파일이 변경된 후 패키지 설치 프로그램에서 실패를 보고했습니다. 복구하려면 업데이트를 다시 설치하지 않고 DevHUD를 다시 시작하세요.", confirmDownload: "이 서명된 업데이트를 다운로드할까요?", confirmInstall: "이 검증된 업데이트의 설치를 승인할까요?", confirmRestart: "검증된 업데이트를 설치하고 지금 DevHUD를 다시 시작할까요?", confirmRetryRestart: "업데이트를 다시 설치하지 않고 DevHUD를 다시 시작할까요?", confirmRecoveryRestart: "업데이트를 다시 설치하지 않고 DevHUD를 다시 시작할까요?",
    confirm: "확인", close: "돌아가기", failed: "업데이트를 완료하지 못했습니다. 설치된 버전은 보존되었습니다.", canceled: "업데이트를 취소했습니다. 설치된 버전은 보존되었습니다.",
  },
} as const;

const updaterDiagnosticCopy = {
  en: {
    offline: "The update service is offline. Your installed version was preserved.", malformed: "The update metadata is malformed. Your installed version was preserved.", "rate-limited": "The update service is rate-limited. Try again later.", missing: "No update exists for this target.", unsupported: "This target or package is unsupported.", canceled: "The update was canceled. Your installed version was preserved.", "invalid-signature": "The update signature is not trusted. Your installed version was preserved.", "rollback-denied": "This update would roll back without authorization and was rejected.", "download-failed": "The update download failed. Your installed version was preserved.", "verification-failed": "The downloaded update failed verification. Your installed version was preserved.", "installation-failed": "Installation failed and the installed version was preserved.", "restart-failed": "Restart failed and the installed version was preserved.",
  },
  ko: {
    offline: "업데이트 서비스에 연결할 수 없습니다. 설치된 버전은 보존되었습니다.", malformed: "업데이트 메타데이터 형식이 잘못되었습니다. 설치된 버전은 보존되었습니다.", "rate-limited": "업데이트 서비스 요청이 제한되었습니다. 나중에 다시 시도하세요.", missing: "이 대상에 사용할 업데이트가 없습니다.", unsupported: "이 대상 또는 패키지는 지원되지 않습니다.", canceled: "업데이트를 취소했습니다. 설치된 버전은 보존되었습니다.", "invalid-signature": "업데이트 서명을 신뢰할 수 없습니다. 설치된 버전은 보존되었습니다.", "rollback-denied": "승인되지 않은 이전 버전 설치를 거부했습니다.", "download-failed": "업데이트 다운로드에 실패했습니다. 설치된 버전은 보존되었습니다.", "verification-failed": "다운로드한 업데이트 검증에 실패했습니다. 설치된 버전은 보존되었습니다.", "installation-failed": "설치에 실패했으며 설치된 버전은 보존되었습니다.", "restart-failed": "다시 시작하지 못했으며 설치된 버전은 보존되었습니다.",
  },
} as const;

type Approval = "download" | "installation" | "restart";
type UpdaterAvailability = "loading" | "supported" | "unsupported";

function downloadCandidateIdentity(status: DesktopUpdaterStatus | null) {
  if (status?.kind !== "available" || !status.candidate) return null;
  return JSON.stringify([status.candidate.version, status.candidate.releaseNotes.en, status.candidate.releaseNotes.ko]);
}

function updaterStatusText(status: DesktopUpdaterStatus, language: SupportedLanguage, copy: (typeof updaterCopy)[SupportedLanguage]) {
  if (status.kind === "restart-required" && status.diagnostic?.code === "installation-failed") return copy.installationUncertain;
  if (status.kind === "restart-required") return copy.restartRequired;
  if (status.diagnostic) return updaterDiagnosticCopy[language][status.diagnostic.code];
  switch (status.kind) {
    case "checking": return copy.checking;
    case "up-to-date": return copy.current;
    case "available": return copy.available;
    case "downloading": return copy.downloading;
    case "downloaded": return copy.downloaded;
    case "installation-approved": return copy.prepared;
    case "failed": return copy.failed;
    case "canceled": return copy.canceled;
    case "restarting": return copy.confirmRestart;
    default: return "";
  }
}

export function DesktopUpdaterPanel({ bridge, language, onApprovalOpenChange }: { readonly bridge: NativeBridgeV1; readonly language: SupportedLanguage; readonly onApprovalOpenChange?: (open: boolean) => void }) {
  const copy = updaterCopy[language];
  const [status, setStatus] = useState<DesktopUpdaterStatus | null>(null);
  const [availability, setAvailability] = useState<UpdaterAvailability>("loading");
  const [approval, setApproval] = useState<Approval | null>(null);
  const statusRevision = useRef(0);
  const approvedDownloadCandidate = useRef<string | null>(null);
  const confirmButton = useRef<HTMLButtonElement>(null);
  const confirmationDialog = useRef<HTMLElement>(null);
  const approvalOpener = useRef<HTMLElement | null>(null);
  const updaterPanel = useRef<HTMLElement>(null);

  function restoreApprovalFocus() {
    const opener = approvalOpener.current;
    approvalOpener.current = null;
    requestAnimationFrame(() => {
      const target = opener?.isConnected ? opener : updaterPanel.current;
      target?.focus();
    });
  }

  const applyStatus = (nextStatus: DesktopUpdaterStatus) => {
    statusRevision.current += 1;
    setAvailability("supported");
    if (approvedDownloadCandidate.current !== null && approvedDownloadCandidate.current !== downloadCandidateIdentity(nextStatus)) {
      approvedDownloadCandidate.current = null;
      onApprovalOpenChange?.(false);
      setApproval((current) => current === "download" ? null : current);
      restoreApprovalFocus();
    }
    setStatus(nextStatus);
  };

  useEffect(() => {
    let active = true;
    let unsubscribe: (() => void) | undefined;
    void bridge.listen((event) => {
      if (active && event.kind === "desktop-update-status") {
        applyStatus(event.status);
      }
    }).then((unlisten) => {
      if (!active) {
        unlisten();
        return;
      }
      unsubscribe = unlisten;
      const requestedAtRevision = statusRevision.current;
      return bridge.request({ operation: "updates.status" }).then((response) => {
        if (active && response.kind === "desktop-update-status" && statusRevision.current === requestedAtRevision) {
          applyStatus(response.status);
        }
      }).catch((error: unknown) => {
        if (active && statusRevision.current === requestedAtRevision && error instanceof NativeBridgeError && error.code === NativeBridgeErrorCode.Unsupported) {
          setAvailability("unsupported");
        }
      });
    }).catch(() => {});
    return () => { active = false; unsubscribe?.(); };
  }, [bridge]);

  useEffect(() => { if (approval) confirmButton.current?.focus(); }, [approval]);
  useEffect(() => () => onApprovalOpenChange?.(false), [onApprovalOpenChange]);

  const request = async (operation: "updates.check" | "updates.approve-download" | "updates.cancel" | "updates.approve-installation" | "updates.approve-restart") => {
    const requestedAtRevision = statusRevision.current;
    const response: NativeBridgeResponseV1 = await bridge.request({ operation });
    if (response.kind === "desktop-update-status" && statusRevision.current === requestedAtRevision) {
      applyStatus(response.status);
    }
  };
  const openApproval = (next: Approval, opener: HTMLElement) => {
    const candidateIdentity = next === "download" ? downloadCandidateIdentity(status) : null;
    if (next === "download" && candidateIdentity === null) return;
    approvedDownloadCandidate.current = candidateIdentity;
    approvalOpener.current = opener;
    onApprovalOpenChange?.(true);
    setApproval(next);
  };
  const closeApproval = () => {
    approvedDownloadCandidate.current = null;
    onApprovalOpenChange?.(false);
    setApproval(null);
    restoreApprovalFocus();
  };
  const handleConfirmationKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "Escape") {
      event.stopPropagation();
      closeApproval();
      return;
    }
    if (event.key !== "Tab") return;
    const focusable = confirmationDialog.current?.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), select:not([disabled]), [href]");
    if (!focusable?.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  const approve = async () => {
    if (approval === "download" && approvedDownloadCandidate.current !== downloadCandidateIdentity(status)) {
      closeApproval();
      return;
    }
    const operation = approval === "download" ? "updates.approve-download" : approval === "installation" ? "updates.approve-installation" : "updates.approve-restart";
    closeApproval();
    await request(operation);
  };
  const installationUncertain = status?.kind === "restart-required" && status.diagnostic?.code === "installation-failed";
  const confirmationText = approval === "download" ? copy.confirmDownload : approval === "installation" ? copy.confirmInstall : installationUncertain ? copy.confirmRecoveryRestart : status?.kind === "restart-required" ? copy.confirmRetryRestart : copy.confirmRestart;

  return <section ref={updaterPanel} className="desktop-updater" aria-labelledby="desktop-updater-title" tabIndex={-1}>
    <h3 id="desktop-updater-title">{copy.title}</h3>
    <p>{copy.summary}</p>
    <dl><dt>{status?.kind === "restart-required" ? copy.running : copy.installed}</dt><dd>{status?.installedVersion ?? "—"}</dd></dl>
    <p className="updater-status" role={status && ["failed", "restart-required"].includes(status.kind) ? "alert" : "status"} aria-live="polite">{status ? updaterStatusText(status, language, copy) : availability === "unsupported" ? updaterDiagnosticCopy[language].unsupported : ""}</p>
    {status?.candidate && <section className="release-notes" aria-labelledby="release-notes-title"><h4 id="release-notes-title">{copy.releaseNotes} · {status.candidate.version}</h4><p>{status.candidate.releaseNotes[language]}</p></section>}
    <div className="actions">
      {availability === "supported" && status && ["idle", "up-to-date", "failed", "canceled"].includes(status.kind) && <button onClick={() => void request("updates.check")}>{copy.check}</button>}
      {status?.kind === "available" && <button className="primary" onClick={(event) => openApproval("download", event.currentTarget)}>{copy.download}</button>}
      {status?.kind === "downloading" && <button onClick={() => void request("updates.cancel")}>{copy.cancel}</button>}
      {status?.kind === "downloaded" && <button className="primary" onClick={(event) => openApproval("installation", event.currentTarget)}>{copy.approveInstall}</button>}
      {status?.kind === "installation-approved" && <button className="primary" onClick={(event) => openApproval("restart", event.currentTarget)}>{copy.restart}</button>}
      {status?.kind === "restart-required" && <button className="primary" onClick={(event) => openApproval("restart", event.currentTarget)}>{installationUncertain ? copy.recoverRestart : copy.retryRestart}</button>}
    </div>
    {approval && <div className="updater-confirmation-backdrop" role="presentation"><section ref={confirmationDialog} className="updater-confirmation" role="dialog" aria-modal="true" aria-labelledby="updater-confirmation-title" onKeyDown={handleConfirmationKeyDown}><h4 id="updater-confirmation-title">{confirmationText}</h4><div className="actions"><button ref={confirmButton} className="primary" onClick={() => void approve()}>{copy.confirm}</button><button onClick={closeApproval}>{copy.close}</button></div></section></div>}
  </section>;
}
