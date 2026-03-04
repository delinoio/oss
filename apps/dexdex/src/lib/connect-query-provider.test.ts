import { describe, expect, it, vi } from "vitest";
import { createBearerTokenInterceptor } from "./connect-query-provider";

describe("createBearerTokenInterceptor", () => {
  it("adds Authorization header when bearer token is present", async () => {
    const next = vi.fn(async (request: { header: Headers }) => request);
    const interceptor = createBearerTokenInterceptor("  token-123  ");
    const request = {
      header: new Headers(),
    };

    await interceptor(next)(request as never);

    expect(request.header.get("Authorization")).toBe("Bearer token-123");
    expect(next).toHaveBeenCalledTimes(1);
  });

  it("does not add Authorization header when bearer token is empty", async () => {
    const next = vi.fn(async (request: { header: Headers }) => request);
    const interceptor = createBearerTokenInterceptor("   ");
    const request = {
      header: new Headers(),
    };

    await interceptor(next)(request as never);

    expect(request.header.get("Authorization")).toBeNull();
    expect(next).toHaveBeenCalledTimes(1);
  });
});
