import { CommitTrackerApp } from "@/apps/commit-tracker/commit-tracker-app";
import { DevkitShell } from "@/components/devkit-shell";
import { DevkitMiniAppId, DevkitRoute } from "@/lib/mini-app-registry";

export default function CommitTrackerPage() {
  return (
    <DevkitShell
      title="Commit Tracker"
      currentRoute={DevkitRoute.CommitTracker}
      miniAppId={DevkitMiniAppId.CommitTracker}
    >
      <CommitTrackerApp />
    </DevkitShell>
  );
}
