import { DevkitShell } from "@/components/devkit-shell";
import { RemoteFilePickerApp } from "@/apps/remote-file-picker/components/remote-file-picker-app";
import {
  DevkitMiniAppId,
  DevkitRoute,
} from "@/lib/mini-app-registry";

export default function RemoteFilePickerPage() {
  return (
    <DevkitShell
      title="Remote File Picker"
      currentRoute={DevkitRoute.RemoteFilePicker}
      miniAppId={DevkitMiniAppId.RemoteFilePicker}
    >
      <RemoteFilePickerApp />
    </DevkitShell>
  );
}
