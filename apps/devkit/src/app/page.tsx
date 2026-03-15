import Link from "next/link";

import { DevkitShell } from "@/components/devkit-shell";
import {
  DevkitRoute,
  MINI_APP_REGISTRATIONS,
  MiniAppStatus,
} from "@/lib/mini-app-registry";

export default function HomePage() {
  return (
    <DevkitShell title="Devkit Home" currentRoute={DevkitRoute.Home}>
      <section>
        <h2 style={{ marginTop: 0 }}>Shell-only bootstrap is active</h2>
        <p>
          Devkit routes are now reserved with enum-based registration and static
          pages for each canonical mini app.
        </p>
        <ul>
          {MINI_APP_REGISTRATIONS.map((app) => (
            <li key={app.id}>
              <Link href={app.route}>
                {app.title} ({app.status === MiniAppStatus.Placeholder
                  ? "placeholder"
                  : app.status}
                )
              </Link>
            </li>
          ))}
        </ul>
      </section>
    </DevkitShell>
  );
}
