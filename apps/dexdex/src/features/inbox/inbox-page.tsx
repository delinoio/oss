import {
  AlertTriangle,
  Bell,
  GitPullRequest,
  Inbox as InboxIcon,
} from "lucide-react";
import { cn } from "../../lib/cn";
import { NotificationType, notificationTypeLabels } from "../../lib/status";
import { formatRelativeTime } from "../../lib/time";
import { mockNotifications } from "../../lib/mock-data";

const notificationIcons: Record<
  NotificationType,
  React.ComponentType<{ size?: number; className?: string }>
> = {
  [NotificationType.UNSPECIFIED]: Bell,
  [NotificationType.TASK_ACTION_REQUIRED]: AlertTriangle,
  [NotificationType.PLAN_ACTION_REQUIRED]: AlertTriangle,
  [NotificationType.PR_REVIEW_ACTIVITY]: GitPullRequest,
  [NotificationType.PR_CI_FAILURE]: AlertTriangle,
  [NotificationType.AGENT_SESSION_FAILED]: AlertTriangle,
};

export function InboxPage() {
  if (mockNotifications.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center h-full gap-3 text-[var(--color-text-tertiary)]">
        <InboxIcon size={32} />
        <span className="text-[13px]">No notifications</span>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      {/* Header */}
      <div className="px-6 py-3 border-b border-[var(--color-border-default)]">
        <h1 className="text-[15px] font-semibold text-[var(--color-text-primary)]">
          Inbox
        </h1>
      </div>

      {/* Notification list */}
      <div className="flex-1 overflow-y-auto">
        {mockNotifications.map((notification) => {
          const Icon = notificationIcons[notification.type] || Bell;
          return (
            <div
              key={notification.notificationId}
              className={cn(
                "flex items-start gap-3 px-6 py-3 border-b border-[var(--color-border-subtle)] cursor-pointer hover:bg-[var(--color-bg-hover)] transition-colors",
                !notification.read && "bg-[var(--color-bg-accent)]/[0.03]",
              )}
            >
              <div className="mt-0.5">
                <Icon
                  size={16}
                  className={cn(
                    notification.read
                      ? "text-[var(--color-text-tertiary)]"
                      : "text-[var(--color-text-accent)]",
                  )}
                />
              </div>
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-2 mb-0.5">
                  <span className="text-[11px] font-medium text-[var(--color-text-tertiary)]">
                    {notificationTypeLabels[notification.type]}
                  </span>
                  {!notification.read && (
                    <span className="w-1.5 h-1.5 rounded-full bg-[var(--color-bg-accent)]" />
                  )}
                </div>
                <p
                  className={cn(
                    "text-[13px] truncate",
                    notification.read
                      ? "text-[var(--color-text-secondary)]"
                      : "text-[var(--color-text-primary)] font-medium",
                  )}
                >
                  {notification.title}
                </p>
                <span className="text-[11px] text-[var(--color-text-tertiary)]">
                  {formatRelativeTime(notification.createdAt)}
                </span>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
