import { DevkitShell } from "@/components/devkit-shell";
import { MiniAppPlaceholder } from "@/components/mini-app-placeholder";
import {
  DevkitMiniAppId,
  DevkitRoute,
  getRequiredMiniAppRegistrationById,
} from "@/lib/mini-app-registry";

const commitTracker = getRequiredMiniAppRegistrationById(
  DevkitMiniAppId.CommitTracker,
);

export default function CommitTrackerPage() {
  return (
    <DevkitShell
      title="Commit Tracker"
      currentRoute={DevkitRoute.CommitTracker}
      miniAppId={DevkitMiniAppId.CommitTracker}
    >
      <MiniAppPlaceholder app={commitTracker} />
    </DevkitShell>
  );
}
