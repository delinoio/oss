"use client";

import Link from "next/link";
import { ReactNode, useMemo, useState } from "react";

import { DevkitRoute, MINI_APP_REGISTRATIONS } from "@/lib/mini-app-registry";

interface ShellNavigationItem {
  readonly key: string;
  readonly title: string;
  readonly route: DevkitRoute;
}

const HOME_NAVIGATION_ITEM: ShellNavigationItem = {
  key: "home",
  title: "Home",
  route: DevkitRoute.Home,
};

export interface DevkitShellLayoutProps {
  title: string;
  currentRoute: DevkitRoute;
  children: ReactNode;
}

export function DevkitShellLayout({
  title,
  currentRoute,
  children,
}: DevkitShellLayoutProps) {
  const [isDrawerOpen, setIsDrawerOpen] = useState<boolean>(false);

  const navigationItems = useMemo(
    () => [
      HOME_NAVIGATION_ITEM,
      ...MINI_APP_REGISTRATIONS.map((registration) => ({
        key: registration.id,
        title: registration.title,
        route: registration.route,
      })),
    ],
    [],
  );

  const closeDrawer = () => {
    setIsDrawerOpen(false);
  };

  const toggleDrawer = () => {
    setIsDrawerOpen((previous) => !previous);
  };

  return (
    <div className={`dk-root ${isDrawerOpen ? "dk-root-drawer-open" : ""}`}>
      <header className="dk-mobile-topbar">
        <button
          type="button"
          className="dk-shell-menu-button"
          aria-label="Toggle mini app navigation menu"
          aria-controls="dk-shell-navigation"
          aria-expanded={isDrawerOpen}
          onClick={toggleDrawer}
        >
          Menu
        </button>
        <p className="dk-mobile-topbar-title">{title}</p>
      </header>

      <aside
        id="dk-shell-navigation"
        className={`dk-sidebar ${isDrawerOpen ? "is-open" : ""}`}
      >
        <div className="dk-sidebar-inner">
          <p className="dk-sidebar-label">Devkit Shell</p>
          <nav aria-label="Mini app navigation">
            <ul className="dk-sidebar-list">
              {navigationItems.map((navigationItem) => (
                <li key={navigationItem.key}>
                  <Link
                    href={navigationItem.route}
                    aria-current={
                      navigationItem.route === currentRoute ? "page" : undefined
                    }
                    className="dk-sidebar-link"
                    onClick={closeDrawer}
                  >
                    {navigationItem.title}
                  </Link>
                </li>
              ))}
            </ul>
          </nav>
        </div>
      </aside>

      <button
        type="button"
        className={`dk-drawer-overlay ${isDrawerOpen ? "is-open" : ""}`}
        aria-label="Close mini app navigation menu"
        aria-hidden={!isDrawerOpen}
        tabIndex={isDrawerOpen ? 0 : -1}
        onClick={closeDrawer}
      />

      <main className="dk-main">
        <div className="dk-main-header">
          <p className="dk-eyebrow">Devkit Shell</p>
          <h1 className="dk-page-title">{title}</h1>
        </div>
        {children}
      </main>
    </div>
  );
}
