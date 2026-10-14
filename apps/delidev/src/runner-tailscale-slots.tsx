// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef } from "react";

// Runner Devices presentation slots for the desktop-owned Tailscale controller.
// The controller itself never mounts here: Settings only offers two empty
// regions, publishes them while the category presentation is mounted, and
// releases them on disposal.
export interface RunnerTailscaleSlots {
  access: HTMLElement;
  devices: HTMLElement;
}
export type PublishRunnerTailscaleSlots = (
  slots: RunnerTailscaleSlots | undefined,
) => void;

export function RunnerTailscalePresentation({
  publish,
}: {
  publish: PublishRunnerTailscaleSlots;
}) {
  const access = useRef<HTMLDivElement>(null),
    devices = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (!access.current || !devices.current) return;
    publish({ access: access.current, devices: devices.current });
    return () => publish(undefined);
  }, [publish]);
  return (
    <div className="runner-tailscale">
      <div ref={access} />
      <div ref={devices} />
    </div>
  );
}
