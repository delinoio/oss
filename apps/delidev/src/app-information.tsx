// SPDX-License-Identifier: Apache-2.0
import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { copy, useLocale } from "./localization";
import type { DesktopUpdateControls } from "./updates";
import "./app-information.css";

export enum AppInformationLink {
  Releases = "releases",
  License = "license",
  Notices = "notices",
}
export type OpenAppInformationLink = (
  link: AppInformationLink,
) => Promise<void>;
const platformKeys = {
  "darwin-amd64": "settings.appInformation.macIntel",
  "darwin-arm64": "settings.appInformation.macApple",
  "windows-amd64": "settings.appInformation.windowsX64",
  "windows-arm64": "settings.appInformation.windowsArm",
  "linux-amd64": "settings.appInformation.linuxX64",
  "linux-arm64": "settings.appInformation.linuxArm",
} as const;
export function appInformationPlatform(target: string) {
  const key = Object.hasOwn(platformKeys, target)
    ? platformKeys[target as keyof typeof platformKeys]
    : undefined;
  return key ? copy(key) : undefined;
}
// One original native read belongs to this product owner, independent of
// disposable Settings visits. A rejected read remains rejected until disposal.
export function retainedDesktopContext(
  read: DesktopUpdateControls["readContext"],
): DesktopUpdateControls["readContext"] {
  let original: ReturnType<DesktopUpdateControls["readContext"]> | undefined;
  return () => (original ??= Promise.resolve().then(read));
}
export function AppInformationSlot({
  onTarget,
}: {
  onTarget: (target: HTMLElement | undefined) => void;
}) {
  const slot = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const original = slot.current;
    if (original) onTarget(original);
    return () => onTarget(undefined);
  }, [onTarget]);
  return <div ref={slot} data-settings-search-pending="true" />;
}
export function AppInformation({
  active = true,
  controls,
  openLink,
  children,
}: {
  active?: boolean;
  controls?: DesktopUpdateControls;
  openLink?: OpenAppInformationLink;
  children?: ReactNode;
}) {
  useLocale();
  const [context, setContext] = useState<{
    current_version: string;
    target: string;
  }>();
  const [contextError, setContextError] = useState(false);
  const [opening, setOpening] = useState<AppInformationLink>();
  const [openError, setOpenError] = useState(false);
  const alive = useRef(true);
  const openingGuard = useRef(false);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  useEffect(() => {
    if (!active || !controls || context || contextError) return;
    let accepted = true;
    void controls
      .readContext()
      .then((value) => {
        if (
          !value.current_version ||
          value.current_version.length > 128 ||
          !appInformationPlatform(value.target)
        )
          throw new Error("Invalid native application context");
        if (accepted) setContext(value);
      })
      .catch(() => {
        if (accepted) setContextError(true);
      });
    return () => {
      accepted = false;
    };
  }, [active, controls, context, contextError]);
  const open = async (link: AppInformationLink) => {
    if (!active || !openLink || openingGuard.current) return;
    openingGuard.current = true;
    setOpening(link);
    setOpenError(false);
    try {
      await openLink(link);
    } catch {
      if (alive.current) setOpenError(true);
    } finally {
      openingGuard.current = false;
      if (alive.current) setOpening(undefined);
    }
  };
  const links = [
    [AppInformationLink.Releases, "settings.appInformation.releases"],
    [AppInformationLink.License, "settings.appInformation.license"],
    [AppInformationLink.Notices, "settings.appInformation.notices"],
  ] as const;
  return (
    <div className="app-information">
      <section
        className="app-information-card"
        aria-label="DeliDev"
        data-settings-search-target="app-information"
      >
        <h2>DeliDev</h2>
        {context ? (
          <dl>
            <dt>{copy("settings.appInformation.version")}</dt>
            <dd>{context.current_version}</dd>
            <dt>{copy("settings.appInformation.platform")}</dt>
            <dd>{appInformationPlatform(context.target)}</dd>
          </dl>
        ) : (
          <p role={contextError ? "alert" : "status"}>
            {copy(
              contextError
                ? "settings.appInformation.contextError"
                : !controls
                  ? "settings.appInformation.unavailable"
                  : "settings.appInformation.loading",
            )}
          </p>
        )}
      </section>
      <section
        className="app-information-card"
        aria-label={copy("settings.appInformation.updates")}
      >
        <h2 data-settings-search-target="app-updates">
          {copy("settings.appInformation.updates")}
        </h2>
        <p className="app-information-help">
          {copy("settings.appInformation.updateHelp")}
        </p>
        {children ?? (
          <p role="status">{copy("settings.appInformation.unavailable")}</p>
        )}
      </section>
      <section
        className="app-information-card"
        aria-label={copy("settings.appInformation.related")}
      >
        <h2>{copy("settings.appInformation.related")}</h2>
        <div className="app-information-links">
          {links.map(([link, label]) => (
            <button
              key={link}
              className="app-information-link"
              type="button"
              data-settings-search-target={`app-${link}`}
              disabled={!active || !openLink || Boolean(opening)}
              onClick={() => void open(link)}
            >
              <span>{copy(label)}</span>
              <svg aria-hidden="true" focusable="false" viewBox="0 0 24 24">
                <path d="M14 4h6v6m0-6-10 10M10 4H4v16h16v-6" />
              </svg>
            </button>
          ))}
        </div>
        <p className="app-information-help">
          {copy("settings.appInformation.sourceNotices")}
        </p>
        {opening ? (
          <p role="status">{copy("settings.appInformation.opening")}</p>
        ) : null}
        {openError ? (
          <p role="alert">{copy("settings.appInformation.openError")}</p>
        ) : null}
      </section>
    </div>
  );
}
