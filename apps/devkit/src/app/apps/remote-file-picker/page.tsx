import { DevkitShell } from "@/components/devkit-shell";
import { MiniAppPlaceholder } from "@/components/mini-app-placeholder";
import {
  DevkitMiniAppId,
  DevkitRoute,
  getRequiredMiniAppRegistrationById,
} from "@/lib/mini-app-registry";

const remoteFilePicker = getRequiredMiniAppRegistrationById(
  DevkitMiniAppId.RemoteFilePicker,
);

export default function RemoteFilePickerPage() {
  return (
    <DevkitShell
      title="Remote File Picker"
      currentRoute={DevkitRoute.RemoteFilePicker}
      miniAppId={DevkitMiniAppId.RemoteFilePicker}
    >
      <MiniAppPlaceholder app={remoteFilePicker} />
    </DevkitShell>
  );
}
