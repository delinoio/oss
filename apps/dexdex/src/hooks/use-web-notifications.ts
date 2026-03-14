/**
 * Hook for Web Notification API integration.
 * Requests permission on mount, dispatches notifications from stream events,
 * and handles deep-link navigation on click.
 */

import { useCallback, useEffect, useRef } from "react";

interface UseWebNotificationsOptions {
  onNavigate: (path: string) => void;
}

/**
 * Set of seen notification keys for dedup: (workspace_id, sequence, notification_type).
 */
const seenNotificationKeys = new Set<string>();

/**
 * Request Web Notification permission on mount.
 * Provides a dispatch function for sending notifications from stream events.
 */
export function useWebNotifications({ onNavigate }: UseWebNotificationsOptions) {
  const permissionRef = useRef<NotificationPermission>("default");

  useEffect(() => {
    if (typeof window === "undefined" || !("Notification" in window)) {
      return;
    }

    if (Notification.permission === "granted") {
      permissionRef.current = "granted";
    } else if (Notification.permission !== "denied") {
      Notification.requestPermission().then((result) => {
        permissionRef.current = result;
        console.log("[WebNotifications] Permission:", result);
      });
    }
  }, []);

  const dispatchNotification = useCallback(
    (params: {
      workspaceId: string;
      sequence: number;
      notificationType: string;
      title: string;
      body: string;
      referenceId?: string;
    }) => {
      if (permissionRef.current !== "granted") return;

      // Dedup by (workspace_id, sequence, notification_type)
      const key = `${params.workspaceId}:${params.sequence}:${params.notificationType}`;
      if (seenNotificationKeys.has(key)) return;
      seenNotificationKeys.add(key);

      const notification = new Notification(params.title, {
        body: params.body,
        tag: key,
      });

      notification.onclick = () => {
        window.focus();
        if (params.referenceId) {
          onNavigate(`/tasks/${params.referenceId}`);
        } else {
          onNavigate("/inbox");
        }
        notification.close();
      };
    },
    [onNavigate],
  );

  return { dispatchNotification };
}
