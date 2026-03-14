/**
 * Inbox page showing notifications and action items.
 */

import type { CSSProperties } from "react";
import type { Notification } from "../../lib/mock-data";
import { NotificationType } from "../../lib/status";

interface InboxPageProps {
  notifications: Notification[];
  onNotificationClick: (notification: Notification) => void;
  onMarkRead: (notificationId: string) => void;
}

function getNotificationIcon(type: NotificationType): string {
  switch (type) {
    case NotificationType.TASK_ACTION_REQUIRED:
      return "!";
    case NotificationType.PLAN_ACTION_REQUIRED:
      return "\u{1F4CB}";
    case NotificationType.PR_REVIEW_ACTIVITY:
      return "\u{1F50D}";
    case NotificationType.PR_CI_FAILURE:
      return "\u26A0";
    case NotificationType.AGENT_SESSION_FAILED:
      return "\u2717";
    default:
      return "\u{1F514}";
  }
}

export function InboxPage({ notifications, onNotificationClick, onMarkRead }: InboxPageProps) {
  const unreadCount = notifications.filter((n) => !n.read).length;

  const containerStyle: CSSProperties = {
    height: "100%",
    display: "flex",
    flexDirection: "column",
    overflow: "hidden",
  };

  const headerStyle: CSSProperties = {
    padding: "var(--space-4) var(--space-6)",
    borderBottom: "1px solid var(--color-border)",
    display: "flex",
    alignItems: "center",
    gap: "var(--space-2)",
    flexShrink: 0,
  };

  const listStyle: CSSProperties = {
    flex: 1,
    overflowY: "auto",
  };

  return (
    <div style={containerStyle} data-testid="inbox-page">
      <div style={headerStyle}>
        <h1 style={{ fontSize: "var(--font-size-xl)", fontWeight: 600 }}>Inbox</h1>
        {unreadCount > 0 && (
          <span
            style={{
              padding: "1px 8px",
              borderRadius: "var(--radius-full)",
              backgroundColor: "var(--color-accent)",
              color: "var(--color-text-inverse)",
              fontSize: "var(--font-size-xs)",
              fontWeight: 600,
            }}
          >
            {unreadCount}
          </span>
        )}
      </div>
      <div style={listStyle}>
        {notifications.length === 0 && (
          <div
            style={{
              padding: "var(--space-8)",
              textAlign: "center",
              color: "var(--color-text-tertiary)",
              fontSize: "var(--font-size-sm)",
            }}
          >
            No notifications
          </div>
        )}
        {notifications.map((notification) => (
          <div
            key={notification.notificationId}
            style={{
              display: "flex",
              alignItems: "flex-start",
              gap: "var(--space-3)",
              padding: "var(--space-3) var(--space-6)",
              borderBottom: "1px solid var(--color-border-subtle)",
              cursor: "pointer",
              backgroundColor: notification.read ? "transparent" : "var(--color-accent-subtle)",
              transition: "background-color 0.1s",
            }}
            onClick={() => {
              onNotificationClick(notification);
              if (!notification.read) {
                onMarkRead(notification.notificationId);
              }
            }}
            onMouseEnter={(e) => {
              (e.currentTarget as HTMLElement).style.backgroundColor = "var(--color-bg-hover)";
            }}
            onMouseLeave={(e) => {
              (e.currentTarget as HTMLElement).style.backgroundColor = notification.read
                ? "transparent"
                : "var(--color-accent-subtle)";
            }}
          >
            <span style={{ fontSize: "var(--font-size-md)", flexShrink: 0, marginTop: "2px" }}>
              {getNotificationIcon(notification.type)}
            </span>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div
                style={{
                  fontSize: "var(--font-size-base)",
                  fontWeight: notification.read ? 400 : 600,
                  color: "var(--color-text-primary)",
                }}
              >
                {notification.title}
              </div>
              <div
                style={{
                  fontSize: "var(--font-size-sm)",
                  color: "var(--color-text-secondary)",
                  marginTop: "2px",
                }}
              >
                {notification.body}
              </div>
            </div>
            {!notification.read && (
              <div
                style={{
                  width: "8px",
                  height: "8px",
                  borderRadius: "50%",
                  backgroundColor: "var(--color-accent)",
                  flexShrink: 0,
                  marginTop: "6px",
                }}
              />
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
