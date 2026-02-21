export enum DevkitMiniAppId {
  CommitTracker = "commit-tracker",
  RemoteFilePicker = "remote-file-picker",
  Thenv = "thenv",
}

export enum DevkitRoute {
  Home = "/",
  CommitTracker = "/apps/commit-tracker",
  RemoteFilePicker = "/apps/remote-file-picker",
  Thenv = "/apps/thenv",
}

export enum MiniAppStatus {
  Placeholder = "placeholder",
}

export enum MiniAppIntegrationMode {
  ShellOnly = "shell-only",
  BackendCoupled = "backend-coupled",
}

export interface MiniAppRegistration {
  id: DevkitMiniAppId;
  title: string;
  route: DevkitRoute;
  status: MiniAppStatus;
  integrationMode: MiniAppIntegrationMode;
  docsPath: string;
}

const miniAppRegistrations: readonly MiniAppRegistration[] = [
  {
    id: DevkitMiniAppId.CommitTracker,
    title: "Commit Tracker",
    route: DevkitRoute.CommitTracker,
    status: MiniAppStatus.Placeholder,
    integrationMode: MiniAppIntegrationMode.BackendCoupled,
    docsPath: "docs/project-devkit-commit-tracker.md",
  },
  {
    id: DevkitMiniAppId.RemoteFilePicker,
    title: "Remote File Picker",
    route: DevkitRoute.RemoteFilePicker,
    status: MiniAppStatus.Placeholder,
    integrationMode: MiniAppIntegrationMode.BackendCoupled,
    docsPath: "docs/project-devkit-remote-file-picker.md",
  },
  {
    id: DevkitMiniAppId.Thenv,
    title: "Thenv",
    route: DevkitRoute.Thenv,
    status: MiniAppStatus.Placeholder,
    integrationMode: MiniAppIntegrationMode.BackendCoupled,
    docsPath: "docs/project-thenv.md",
  },
];

export function validateMiniAppRegistrations(
  registrations: readonly MiniAppRegistration[],
): void {
  const seenIds = new Set<DevkitMiniAppId>();
  const seenRoutes = new Set<DevkitRoute>();

  for (const registration of registrations) {
    if (seenIds.has(registration.id)) {
      throw new Error(`Duplicate mini app id: ${registration.id}`);
    }
    seenIds.add(registration.id);

    if (seenRoutes.has(registration.route)) {
      throw new Error(`Duplicate mini app route: ${registration.route}`);
    }
    seenRoutes.add(registration.route);
  }
}

validateMiniAppRegistrations(miniAppRegistrations);

export const MINI_APP_REGISTRATIONS = miniAppRegistrations;

export function getMiniAppRegistrationById(
  miniAppId: DevkitMiniAppId,
): MiniAppRegistration | undefined {
  return MINI_APP_REGISTRATIONS.find((registration) => registration.id === miniAppId);
}

export function getRequiredMiniAppRegistrationById(
  miniAppId: DevkitMiniAppId,
): MiniAppRegistration {
  const registration = getMiniAppRegistrationById(miniAppId);
  if (!registration) {
    throw new Error(`Missing mini app registration for id: ${miniAppId}`);
  }
  return registration;
}
