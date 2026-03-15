/**
 * Inbox page showing notifications and action items.
 * Uses Connect RPC queries directly for notification data.
 */

import { type CSSProperties, useCallback, useContext } from "react";
import { useNavigate } from "react-router";
import { InboxSkeleton } from "../../components/skeleton-loader";
import { useListNotifications, useMarkNotificationReadMutation } from "../../hooks/use-dexdex-queries";
import { AppStoreContext } from "../../stores/app-store";
import { NotificationType } from "../../lib/status";
import { formatRelativeTime } from "../../lib/time";
import type { Notification } from "../../lib/mock-data";

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
    case NotificationType.AGENT_INPUT_REQUIRED:
      return "\u{1F4AC}";
    default:
      return "\u{1F514}";
  }
}

function getNotificationBadgeColor(type: NotificationType): string {
  switch (type) {
    case NotificationType.TASK_ACTION_REQUIRED:
    case NotificationType.PLAN_ACTION_REQUIRED:
      return "var(--color-status-action)";
    case NotificationType.PR_CI_FAILURE:
    case NotificationType.AGENT_SESSION_FAILED:
      return "var(--color-status-failed)";
    case NotificationType.PR_REVIEW_ACTIVITY:
      return "var(--color-status-in-progress)";
    case NotificationType.AGENT_INPUT_REQUIRED:
      return "var(--color-status-action)";
    default:
      return "var(--color-text-tertiary)";
  }
}

export function InboxPage() {
  const navigate = useNavigate();
  const store = useContext(AppStoreContext);
  const workspaceId = store?.activeWorkspaceId ?? "workspace-default";

  const { data: notifications = [], isLoading } = useListNotifications(workspaceId);
  const markReadMutation = useMarkNotificationReadMutation();

  const unreadCount = notifications.filter((n) => !n.read).length;

  const handleNotificationClick = useCallback(
    (notification: Notification) => {
      // Mark as read if unread
      if (!notification.read) {
        markReadMutation.mutate({ workspaceId, notificationId: notification.notificationId });
      }
      // Navigate to referenced task or PR
      if (notification.taskId) {
        navigate(`/tasks/${notification.taskId}`);
      }
    },
    [navigate, markReadMutation, workspaceId],
  );

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
        {isLoading ? (
          <InboxSkeleton />
        ) : notifications.length === 0 ? (
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              justifyContent: "center",
              padding: "var(--space-12) var(--space-8)",
              textAlign: "center",
              color: "var(--color-text-tertiary)",
              gap: "var(--space-2)",
            }}
          >
            <span style={{ fontSize: "32px", opacity: 0.5 }}>{"\u{1F4EC}"}</span>
            <div style={{ fontSize: "var(--font-size-base)", fontWeight: 500 }}>No notifications</div>
            <div style={{ fontSize: "var(--font-size-sm)" }}>
              You're all caught up. Notifications for tasks, plans, and PRs will appear here.
            </div>
          </div>
        ) : null}
        {!isLoading &&
          notifications.map((notification) => (
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
              onClick={() => handleNotificationClick(notification)}
              onMouseEnter={(e) => {
                (e.currentTarget as HTMLElement).style.backgroundColor = "var(--color-bg-hover)";
              }}
              onMouseLeave={(e) => {
                (e.currentTarget as HTMLElement).style.backgroundColor = notification.read
                  ? "transparent"
                  : "var(--color-accent-subtle)";
              }}
            >
              <span
                style={{
                  fontSize: "var(--font-size-md)",
                  flexShrink: 0,
                  marginTop: "2px",
                  width: "24px",
                  height: "24px",
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  borderRadius: "var(--radius-sm)",
                  backgroundColor: getNotificationBadgeColor(notification.type),
                  color: "var(--color-text-inverse)",
                  fontSize: "var(--font-size-xs)",
                  fontWeight: 700,
                }}
              >
                {getNotificationIcon(notification.type)}
              </span>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div
                  style={{
                    display: "flex",
                    alignItems: "baseline",
                    gap: "var(--space-2)",
                  }}
                >
                  <span
                    style={{
                      fontSize: "var(--font-size-base)",
                      fontWeight: notification.read ? 400 : 600,
                      color: "var(--color-text-primary)",
                    }}
                  >
                    {notification.title}
                  </span>
                  <span
                    style={{
                      fontSize: "var(--font-size-xs)",
                      color: "var(--color-text-tertiary)",
                      flexShrink: 0,
                    }}
                  >
                    {formatRelativeTime(notification.createdAt)}
                  </span>
                </div>
                <div
                  style={{
                    fontSize: "var(--font-size-sm)",
                    color: "var(--color-text-secondary)",
                    marginTop: "2px",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
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
