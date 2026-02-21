import { ThenvConsole } from "@/apps/thenv/thenv-console";
import { DevkitShell } from "@/components/devkit-shell";
import { DevkitMiniAppId, DevkitRoute } from "@/lib/mini-app-registry";

export default function ThenvPage() {
  return (
    <DevkitShell
      title="Thenv Console"
      currentRoute={DevkitRoute.Thenv}
      miniAppId={DevkitMiniAppId.Thenv}
    >
      <ThenvConsole />
    </DevkitShell>
  );
}
