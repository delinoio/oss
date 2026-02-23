import { PickerSource } from "@/apps/remote-file-picker/contracts";

export interface SourceAdapter {
  source: PickerSource;
  buttonLabel: string;
  description: string;
  inputCapture?: "environment";
}

const PHASE_ONE_ADAPTERS: Record<PickerSource.LocalFile | PickerSource.MobileCamera, SourceAdapter> = {
  [PickerSource.LocalFile]: {
    source: PickerSource.LocalFile,
    buttonLabel: "Choose local file",
    description: "Select a file from this device.",
  },
  [PickerSource.MobileCamera]: {
    source: PickerSource.MobileCamera,
    buttonLabel: "Capture from camera",
    description: "Open camera capture flow on supported devices.",
    inputCapture: "environment",
  },
};

export function getPhaseOneSourceAdapters(
  allowedSources: readonly PickerSource[],
): SourceAdapter[] {
  return allowedSources
    .filter(
      (source): source is PickerSource.LocalFile | PickerSource.MobileCamera =>
        source === PickerSource.LocalFile || source === PickerSource.MobileCamera,
    )
    .map((source) => PHASE_ONE_ADAPTERS[source]);
}
