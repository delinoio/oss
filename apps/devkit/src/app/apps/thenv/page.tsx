import { DevkitShell } from "@/components/devkit-shell";
import { ThenvApp } from "@/apps/thenv/components/thenv-app";
import {
  DevkitMiniAppId,
  DevkitRoute,
} from "@/lib/mini-app-registry";

export default function ThenvPage() {
  return (
    <DevkitShell
      title="Thenv Console"
      currentRoute={DevkitRoute.Thenv}
      miniAppId={DevkitMiniAppId.Thenv}
    >
      <ThenvApp />
    </DevkitShell>
  );
}
