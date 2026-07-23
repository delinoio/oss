import { useSyncExternalStore } from "react";

function subscribe(listener: () => void): () => void {
  window.addEventListener("online", listener);
  window.addEventListener("offline", listener);
  return () => {
    window.removeEventListener("online", listener);
    window.removeEventListener("offline", listener);
  };
}

function snapshot(): boolean {
  return navigator.onLine;
}

export function useOnline(): boolean {
  return useSyncExternalStore(subscribe, snapshot, () => true);
}
