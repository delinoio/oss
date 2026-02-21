import { DevkitShell } from "@/components/devkit-shell";
import { MiniAppPlaceholder } from "@/components/mini-app-placeholder";
import {
  DevkitMiniAppId,
  DevkitRoute,
  getRequiredMiniAppRegistrationById,
} from "@/lib/mini-app-registry";

const thenv = getRequiredMiniAppRegistrationById(DevkitMiniAppId.Thenv);

export default function ThenvPage() {
  return (
    <DevkitShell
      title="Thenv Console"
      currentRoute={DevkitRoute.Thenv}
      miniAppId={DevkitMiniAppId.Thenv}
    >
      <MiniAppPlaceholder app={thenv} />
    </DevkitShell>
  );
}
