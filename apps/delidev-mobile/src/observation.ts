// SPDX-License-Identifier: Apache-2.0
import {
  createContext,
  useContext,
  useState,
  useRef,
  type SetStateAction,
} from "react";

export const ObservationEpoch = createContext(0);

// Keep owner-local observations tied to the epoch that read them. Drafts use ordinary state.
export function useObservationState<T>(initial: T) {
  const epoch = useContext(ObservationEpoch);
  const currentEpoch = useRef(epoch);
  currentEpoch.current = epoch;
  const [snapshot, setSnapshot] = useState({ epoch, value: initial });
  if (snapshot.epoch !== epoch) setSnapshot({ epoch, value: initial });
  const value = snapshot.epoch === epoch ? snapshot.value : initial;
  const set = (next: SetStateAction<T>) => {
    if (currentEpoch.current !== epoch) return;
    setSnapshot((previous) => ({
      epoch,
      value:
        typeof next === "function"
          ? (next as (value: T) => T)(
              previous.epoch === epoch ? previous.value : initial,
            )
          : next,
    }));
  };
  return [value, set] as const;
}
