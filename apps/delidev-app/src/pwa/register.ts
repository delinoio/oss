export const SERVICE_WORKER_UPDATE_EVENT = "delidev:service-worker-update";

export async function registerServiceWorker(): Promise<void> {
  if (!("serviceWorker" in navigator) || !window.isSecureContext) {
    return;
  }

  const registration = await navigator.serviceWorker.register("/sw.js", {
    scope: "/",
  });

  const announceWaitingWorker = () => {
    if (registration.waiting) {
      window.dispatchEvent(new Event(SERVICE_WORKER_UPDATE_EVENT));
    }
  };

  announceWaitingWorker();
  let refreshing = false;
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    if (!refreshing) {
      refreshing = true;
      window.location.reload();
    }
  });
  registration.addEventListener("updatefound", () => {
    registration.installing?.addEventListener("statechange", () => {
      if (
        registration.installing?.state === "installed" &&
        navigator.serviceWorker.controller
      ) {
        announceWaitingWorker();
      }
    });
  });

  window.setInterval(() => void registration.update(), 60 * 60 * 1000);
}

export async function activateWaitingServiceWorker(): Promise<void> {
  const registration = await navigator.serviceWorker.getRegistration("/");
  registration?.waiting?.postMessage({ type: "SKIP_WAITING" });
}
