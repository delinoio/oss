export type HostedBillingNavigator = (url: string) => void;

export function hostedBillingReturnUrl(value: string): string {
  const url = new URL(value);
  url.hash = "";
  return url.href;
}

export function polarHostedUrl(value: string): string | undefined {
  try {
    const url = new URL(value);
    const polarHost =
      url.hostname === "polar.sh" || url.hostname.endsWith(".polar.sh");
    if (
      url.protocol !== "https:" ||
      !polarHost ||
      url.username ||
      url.password
    ) {
      return undefined;
    }
    return url.href;
  } catch {
    return undefined;
  }
}

export function navigateToPolarHostedPage(
  value: string,
  navigate: HostedBillingNavigator = (url) => window.location.assign(url),
): boolean {
  const url = polarHostedUrl(value);
  if (!url) return false;
  navigate(url);
  return true;
}
