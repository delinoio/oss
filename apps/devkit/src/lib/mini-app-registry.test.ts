import {
  DevkitMiniAppId,
  DevkitRoute,
  MINI_APP_REGISTRATIONS,
  MiniAppStatus,
  validateMiniAppRegistrations,
} from "@/lib/mini-app-registry";

describe("mini-app-registry", () => {
  it("keeps canonical mini app id values aligned with docs", () => {
    expect(Object.values(DevkitMiniAppId)).toEqual([
      "commit-tracker",
      "remote-file-picker",
      "thenv",
    ]);
  });

  it("keeps canonical routes aligned with docs", () => {
    expect(Object.values(DevkitRoute)).toEqual([
      "/",
      "/apps/commit-tracker",
      "/apps/remote-file-picker",
      "/apps/thenv",
    ]);
  });

  it("keeps thenv as live while other mini apps remain placeholders", () => {
    const thenvRegistration = MINI_APP_REGISTRATIONS.find(
      (registration) => registration.id === DevkitMiniAppId.Thenv,
    );
    expect(thenvRegistration?.status).toBe(MiniAppStatus.Live);

    const nonThenvRegistrations = MINI_APP_REGISTRATIONS.filter(
      (registration) => registration.id !== DevkitMiniAppId.Thenv,
    );
    expect(
      nonThenvRegistrations.every(
        (registration) => registration.status === MiniAppStatus.Placeholder,
      ),
    ).toBe(true);
  });

  it("validates unique ids and routes", () => {
    expect(() => validateMiniAppRegistrations(MINI_APP_REGISTRATIONS)).not.toThrow();
  });

  it("fails when duplicate routes are provided", () => {
    const duplicatedRouteRegistrations = [
      MINI_APP_REGISTRATIONS[0],
      {
        ...MINI_APP_REGISTRATIONS[1],
        route: MINI_APP_REGISTRATIONS[0].route,
      },
    ];

    expect(() =>
      validateMiniAppRegistrations(duplicatedRouteRegistrations),
    ).toThrow("Duplicate mini app route");
  });
});
