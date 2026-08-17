import { describe, expect, it } from "vitest";
import { ADMIN_CSP, developmentAdminCsp } from "./csp";

describe("administrator content security policy", () => {
  it("allows the exact configured loopback Logto issuer origin", () => {
    const csp = developmentAdminCsp("http://localhost:3001/oidc");
    expect(csp).toContain(
      "connect-src 'self' http://127.0.0.1:46307 http://localhost:3001",
    );
    expect(csp).not.toContain("/oidc");
  });

  it("allows only the exact configured HTTPS issuer", () => {
    const csp = developmentAdminCsp("https://issuer.example/oidc");
    expect(csp).toContain(
      "connect-src 'self' http://127.0.0.1:46307 https://issuer.example",
    );
    expect(csp).not.toMatch(/connect-src[^;]*(?:^|\s)https:(?:\s|;)/u);
    expect(ADMIN_CSP).toContain("connect-src 'self' http://127.0.0.1:46307");
    expect(ADMIN_CSP).not.toMatch(/connect-src[^;]*(?:^|\s)https:(?:\s|;)/u);
  });

  it("rejects missing or unsafe development issuers", () => {
    expect(() => developmentAdminCsp(undefined)).toThrow(/required/u);
    expect(() => developmentAdminCsp("http://issuer.example/oidc")).toThrow(
      /HTTPS outside loopback/u,
    );
    expect(() => developmentAdminCsp("file:///tmp/issuer")).toThrow(/HTTP or HTTPS/u);
  });
});
