// Experimental publication is deliberately limited to the first release line.
// Broaden this format only through a reviewed release-contract change.
export function isPreviewVersion(version) {
  if (typeof version !== "string" || version.length > 62 || !/^0\.1\.0-next\.([1-9]\d*)$/u.test(version)) return false;
  return BigInt(version.slice("0.1.0-next.".length)) <= 18446744073709551615n;
}

export function publicationChannel(version) {
  if (isPreviewVersion(version)) return { channel: "next", prerelease: true };
  if (typeof version === "string" && /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u.test(version)) return { channel: "latest", prerelease: false };
  throw new Error("Unsupported pnport publication version");
}
