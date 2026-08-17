import { describe, expect, it } from "vitest";
import { ADMIN_CSP, developmentAdminCsp } from "./csp";

describe("administrator content security policy", () => {
  it("allows the exact configured loopback Logto issuer origin", () => {
    const csp = developmentAdminCsp("http://localhost:3001/oidc");
    expect(csp).toContain(
      "connect-src 'self' http://127.0.0.1:46307 https: http://localhost:3001",
    );
    expect(csp).not.toContain("/oidc");
  });

  it("keeps HTTPS issuers within the existing scheme allowlist", () => {
    expect(developmentAdminCsp("https://issuer.example/oidc")).toContain(
      "connect-src 'self' http://127.0.0.1:46307 https: https://issuer.example",
    );
    expect(ADMIN_CSP).toContain("connect-src 'self' http://127.0.0.1:46307 https:");
  });

  it("rejects missing or unsafe development issuers", () => {
    expect(() => developmentAdminCsp(undefined)).toThrow(/required/u);
    expect(() => developmentAdminCsp("http://issuer.example/oidc")).toThrow(
      /HTTPS outside loopback/u,
    );
    expect(() => developmentAdminCsp("file:///tmp/issuer")).toThrow(/HTTP or HTTPS/u);
  });
});
