import { expect, test } from "@playwright/test";

const desktopStates: ReadonlyArray<{ path: string; screenshot: string; readyText: string }> = [
  { path: "/projects?visual=1", screenshot: "desktop-projects.png", readyText: "Projects" },
  { path: "/threads?visual=1", screenshot: "desktop-threads.png", readyText: "Threads" },
  { path: "/review?visual=1", screenshot: "desktop-review.png", readyText: "Review" },
  { path: "/automations?visual=1", screenshot: "desktop-automations.png", readyText: "Automations" },
  { path: "/worktrees?visual=1", screenshot: "desktop-worktrees.png", readyText: "Worktrees" },
  {
    path: "/local-environments?visual=1",
    screenshot: "desktop-local-environments.png",
    readyText: "Local Environments",
  },
  { path: "/settings?visual=1", screenshot: "desktop-settings.png", readyText: "Settings" },
];

test("workspace picker visual baseline", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("Select a workspace to get started.")).toBeVisible();
  await expect(page).toHaveScreenshot("workspace-picker.png", { fullPage: true });
});

for (const state of desktopStates) {
  test(`desktop visual baseline: ${state.path}`, async ({ page }) => {
    await page.goto(state.path);
    await expect(page.getByRole("heading", { name: state.readyText })).toBeVisible();
    await expect(page).toHaveScreenshot(state.screenshot, { fullPage: true });
  });
}
