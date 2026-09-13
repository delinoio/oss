import type { SVGProps } from "react";

export type IconProps = Omit<SVGProps<SVGSVGElement>, "children">;

function Icon({ children, ...props }: IconProps & { readonly children: React.ReactNode }) {
  return <svg {...props} aria-hidden="true" focusable="false" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">{children}</svg>;
}

export function HomeIcon(props: IconProps) { return <Icon {...props}><path d="m3 11 9-8 9 8" /><path d="M5 10v10h14V10" /><path d="M9 20v-6h6v6" /></Icon>; }
export function RealqaIcon(props: IconProps) { return <Icon {...props}><rect x="3" y="6" width="18" height="13" rx="3" /><path d="m8 6 1.4-2h5.2L16 6" /><circle cx="12" cy="12.5" r="3.25" /></Icon>; }
export function DeckIcon(props: IconProps) { return <Icon {...props}><rect x="4" y="3" width="16" height="5" rx="2" /><rect x="4" y="10" width="16" height="5" rx="2" /><rect x="4" y="17" width="16" height="4" rx="2" /></Icon>; }
export function SettingsIcon(props: IconProps) { return <Icon {...props}><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .34 1.88l.06.06-2.83 2.83-.06-.06A1.7 1.7 0 0 0 15 19.4a1.7 1.7 0 0 0-1 .6 1.7 1.7 0 0 0-.4 1.1V21h-4v-.1A1.7 1.7 0 0 0 8.5 19.4a1.7 1.7 0 0 0-1.88.34l-.06.06-2.83-2.83.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-.6-1 1.7 1.7 0 0 0-1.1-.4H3v-4h.1A1.7 1.7 0 0 0 4.6 8.5a1.7 1.7 0 0 0-.34-1.88l-.06-.06 2.83-2.83.06.06A1.7 1.7 0 0 0 9 4.6a1.7 1.7 0 0 0 1-.6 1.7 1.7 0 0 0 .4-1.1V3h4v.1A1.7 1.7 0 0 0 15.5 4.6a1.7 1.7 0 0 0 1.88-.34l.06-.06 2.83 2.83-.06.06A1.7 1.7 0 0 0 19.4 9c.2.38.52.7.9.9.3.16.66.23 1 .2h.1v4h-.1a1.7 1.7 0 0 0-1.9.9Z" /></Icon>; }
export function AccountIcon(props: IconProps) { return <Icon {...props}><circle cx="12" cy="8" r="4" /><path d="M4 21a8 8 0 0 1 16 0" /></Icon>; }
export function DiagnosticsIcon(props: IconProps) { return <Icon {...props}><path d="M4 19V9" /><path d="M10 19V5" /><path d="M16 19v-7" /><path d="M22 19V3" /><path d="M2 19h20" /></Icon>; }
export function MoreIcon(props: IconProps) { return <Icon {...props}><circle cx="5" cy="12" r="1" fill="currentColor" stroke="none" /><circle cx="12" cy="12" r="1" fill="currentColor" stroke="none" /><circle cx="19" cy="12" r="1" fill="currentColor" stroke="none" /></Icon>; }
export function SearchIcon(props: IconProps) { return <Icon {...props}><circle cx="11" cy="11" r="7" /><path d="m20 20-4-4" /></Icon>; }
export function ArrowRightIcon(props: IconProps) { return <Icon {...props}><path d="M5 12h14" /><path d="m14 7 5 5-5 5" /></Icon>; }
export function ArrowDownIcon(props: IconProps) { return <Icon {...props}><path d="m6 9 6 6 6-6" /></Icon>; }
export function InfoIcon(props: IconProps) { return <Icon {...props}><circle cx="12" cy="12" r="9" /><path d="M12 11v5" /><path d="M12 8h.01" /></Icon>; }
export function SuccessIcon(props: IconProps) { return <Icon {...props}><circle cx="12" cy="12" r="9" /><path d="m8 12 2.5 2.5L16 9" /></Icon>; }
export function WarningIcon(props: IconProps) { return <Icon {...props}><path d="M10.3 4.2 2.7 18a2 2 0 0 0 1.8 3h15a2 2 0 0 0 1.8-3L13.7 4.2a2 2 0 0 0-3.4 0Z" /><path d="M12 9v4" /><path d="M12 17h.01" /></Icon>; }
export function ErrorIcon(props: IconProps) { return <Icon {...props}><circle cx="12" cy="12" r="9" /><path d="m9 9 6 6" /><path d="m15 9-6 6" /></Icon>; }
export function AppearanceIcon(props: IconProps) { return <Icon {...props}><circle cx="12" cy="12" r="9" /><path d="M12 3a9 9 0 0 0 0 18c1.1 0 1.8-.9 1.4-1.9-.5-1.2.4-2.5 1.7-2.5h1.4A4.5 4.5 0 0 0 21 12 9 9 0 0 0 12 3Z" /><circle cx="8" cy="9" r=".7" fill="currentColor" /><circle cx="12" cy="7" r=".7" fill="currentColor" /><circle cx="16" cy="10" r=".7" fill="currentColor" /></Icon>; }
export function ShortcutIcon(props: IconProps) { return <Icon {...props}><rect x="3" y="5" width="18" height="14" rx="3" /><path d="M7 9h2M12 9h2M17 9h.01M7 13h.01M10 13h7" /></Icon>; }
export function ChromeExtensionIcon(props: IconProps) { return <Icon {...props}><path d="M8 3h4v4h4V3h2a3 3 0 0 1 3 3v3h-4v4h4v5a3 3 0 0 1-3 3H6a3 3 0 0 1-3-3V6a3 3 0 0 1 3-3h2Z" /></Icon>; }
export function LinkIcon(props: IconProps) { return <Icon {...props}><path d="m9 15 6-6" /><path d="M7.5 18.5 5 21a4.2 4.2 0 0 1-6-6l3-3a4.2 4.2 0 0 1 6 0" transform="translate(3 -3)" /><path d="m16.5 5.5 2.5-2.5a4.2 4.2 0 0 1 6 6l-3 3a4.2 4.2 0 0 1-6 0" transform="translate(-4 3)" /></Icon>; }
export function GitHubIcon(props: IconProps) { return <Icon {...props}><path d="M8 19c-4 1.2-4-2-5-2.5M16 21v-3.1c0-.9.3-1.6.8-2.1 2.7-.3 5.5-1.3 5.5-6A4.7 4.7 0 0 0 21 6.5 4.4 4.4 0 0 0 20.9 3S19.8 2.7 17 4.3a12 12 0 0 0-6 0C8.2 2.7 7.1 3 7.1 3A4.4 4.4 0 0 0 7 6.5a4.7 4.7 0 0 0-1.3 3.3c0 4.7 2.8 5.7 5.5 6-.4.4-.7 1-.8 1.7V21" /></Icon>; }
export function CloudIcon(props: IconProps) { return <Icon {...props}><path d="M7 19h11a4 4 0 0 0 .5-8A7 7 0 0 0 5 9.5 4.8 4.8 0 0 0 7 19Z" /></Icon>; }
export function LocalAgentIcon(props: IconProps) { return <Icon {...props}><rect x="4" y="7" width="16" height="12" rx="3" /><path d="M9 12h.01M15 12h.01M9 16h6M12 3v4" /></Icon>; }
export function NotificationIcon(props: IconProps) { return <Icon {...props}><path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4" /></Icon>; }
export function UpdateIcon(props: IconProps) { return <Icon {...props}><path d="M20 7v5h-5" /><path d="M4 17v-5h5" /><path d="M6.1 8a7 7 0 0 1 11.5-2.4L20 8M4 16l2.4 2.4A7 7 0 0 0 17.9 16" /></Icon>; }
