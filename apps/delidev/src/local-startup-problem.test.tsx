// SPDX-License-Identifier: Apache-2.0
import { act, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { i18n } from "./localization";
import { localStartupProblem } from "./local-startup-problem";
import { LocalServerState, LocalServerStatusText } from "./local-server";

it("shares bounded localized conflict guidance across startup and diagnostics", async () => {
 for (const language of ["en", "ko"]) {
  await act(() => i18n.changeLanguage(language));
  for (const failure of ["ownership-conflict", "startup-conflict", "incompatible"]) {
   const view = render(<LocalServerStatusText status={{ state: LocalServerState.Blocked, attempts: 1, retry_ms: 0, failure }} />);
   expect(screen.getByRole("status").textContent).toBe(localStartupProblem(failure));
   if (failure.endsWith("conflict")) expect(screen.getByRole("status").textContent).toContain(language === "en" ? "confirmed shutdown" : "종료를 확인");
   view.rerender(<LocalServerStatusText status={{ state: LocalServerState.Ready, attempts: 0, retry_ms: 0 }} />);
   expect(screen.getByRole("status").textContent).not.toBe(localStartupProblem(failure));
   view.unmount();
  }
 }
 expect(localStartupProblem("private-path-secret")).toBeUndefined();
 await act(() => i18n.changeLanguage("en"));
});
