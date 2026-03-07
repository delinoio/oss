export type SharedSelectionState = {
  selectedUnitTaskId: string | null;
  selectedSubTaskId: string | null;
  selectedSessionId: string | null;
  selectedPrTrackingId: string | null;
};

export function createEmptySharedSelectionState(): SharedSelectionState {
  return {
    selectedUnitTaskId: null,
    selectedSubTaskId: null,
    selectedSessionId: null,
    selectedPrTrackingId: null,
  };
}
