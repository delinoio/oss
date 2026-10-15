// SPDX-License-Identifier: Apache-2.0
import { useLayoutEffect, useRef } from "react";

// Tailscale category presentation slots for the desktop-owned Tailscale
// controller. The controller itself never mounts here: Settings only offers
// three empty regions (This computer, Devices on your tailnet and Connection
// requests), publishes them while the Tailscale category presentation is
// mounted, and releases them on disposal.
export interface RunnerTailscaleSlots {
  access: HTMLElement;
  devices: HTMLElement;
  requests: HTMLElement;
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
    devices = useRef<HTMLDivElement>(null),
    requests = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (!access.current || !devices.current || !requests.current) return;
    publish({
      access: access.current,
      devices: devices.current,
      requests: requests.current,
    });
    return () => publish(undefined);
  }, [publish]);
  return (
    <div className="runner-tailscale">
      <div ref={access} />
      <div ref={devices} />
      <div ref={requests} />
    </div>
  );
}
