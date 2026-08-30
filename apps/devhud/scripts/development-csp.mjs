import { devhudFrontendContract } from "../../../scripts/dev-environment/contracts.mjs";

const fixedDevelopmentConnectSources = Object.freeze([
  "'self'",
  "http://127.0.0.1:46307",
  "https://devhud.api.delino.io",
  "https://api.github.com",
]);

export function developmentLogtoOrigin(value) {
  if (!devhudFrontendContract.required.DEVHUD_LOGTO_ISSUER(value)) {
    throw new TypeError("DevHud development Logto issuer is invalid");
  }
  return new URL(value).origin;
}

export function createDevHudDevelopmentCsp(logtoIssuer) {
  const connectSources = [
    ...fixedDevelopmentConnectSources,
    developmentLogtoOrigin(logtoIssuer),
    "ws://127.0.0.1:46305",
  ].filter((source, index, sources) => sources.indexOf(source) === index);
  return [
    "default-src 'self'",
    "script-src 'self'",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: realqa: http://realqa.localhost",
    "font-src 'self'",
    `connect-src ${connectSources.join(" ")}`,
    "object-src 'none'",
    "base-uri 'none'",
    "form-action 'none'",
    "frame-src 'none'",
    "worker-src 'none'",
  ].join("; ");
}
