import { DevkitShell } from "@/components/devkit-shell";
import { CommitTrackerApp } from "@/apps/commit-tracker/components/commit-tracker-app";
import {
  DevkitMiniAppId,
  DevkitRoute,
} from "@/lib/mini-app-registry";

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
