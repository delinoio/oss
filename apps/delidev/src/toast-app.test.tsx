// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport, type Transport } from "@connectrpc/connect";
import { act, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ResourceService, SessionService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { App } from "./App";
import { ToastKind, useNotifications, type NotificationController } from "./toast-notifications";

it("retains App notifications across authenticated reconnect and clears them on device replacement", async () => {
  const serverId = newRequestId(), deviceId = newRequestId();
  const transport = () => createRouterTransport(router => {
    router.service(SessionService, { listSessions: () => ({ sessions: [] }) });
    router.service(ResourceService, { listResources: () => ({ resources: [] }) });
    router.service(SystemService, { getStatus: () => ({ serverId, protocolVersion: 2 }) });
  });
  const target = document.createElement("div"); document.body.append(target);
  let controller!: NotificationController;
  function Consumer() { controller = useNotifications(); return null; }
  const view = (upstream: Transport, currentDeviceId = deviceId) => <App transport={upstream} currentDeviceId={currentDeviceId} pairingAuthority={{ endpoint: "http://127.0.0.1:46310", serverId }} connectionTarget={target} localServer={<Consumer />} />;
  const initial = transport(), mounted = render(view(initial));
  try {
    act(() => { controller.notify({ kind: ToastKind.Success, message: "Retained save", durationMs: 0 }); });
    await act(async () => { mounted.rerender(view(transport())); });
    expect(screen.getByText("Retained save")).toBeTruthy();
    const original = controller;
    await act(async () => { mounted.rerender(view(transport(), newRequestId())); });
    expect(screen.queryByText("Retained save")).toBeNull();
    act(() => { original.notify({ kind: ToastKind.Success, message: "Foreign late save" }); });
    expect(screen.queryByText("Foreign late save")).toBeNull();
    expect(document.querySelectorAll(".toast-viewport")).toHaveLength(1);
    expect(screen.queryByRole("region", { name: "Notifications" })).toBeNull();
  } finally { mounted.unmount(); target.remove(); }
});
