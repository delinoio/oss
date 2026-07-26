import { describe, expect, it, vi } from "vitest";

import {
  navigateToPolarHostedPage,
  polarHostedUrl,
} from "../utils/hostedBilling";

describe("Polar-hosted billing navigation", () => {
  it("navigates only to HTTPS Polar checkout and portal pages", () => {
    const navigate = vi.fn();

    expect(
      navigateToPolarHostedPage(
        "https://checkout.polar.sh/session/checkout-id",
        navigate,
      ),
    ).toBe(true);
    expect(navigate).toHaveBeenCalledWith(
      "https://checkout.polar.sh/session/checkout-id",
    );
    expect(
      navigateToPolarHostedPage(
        "https://sandbox.polar.sh/customer-portal/session-id",
        navigate,
      ),
    ).toBe(true);
  });

  it.each([
    "http://checkout.polar.sh/session",
    "https://polar.sh.evil.example/session",
    "https://user:secret@polar.sh/session",
    "javascript:alert(1)",
    "",
  ])("rejects non-Polar or unsafe session URL %s", (value) => {
    const navigate = vi.fn();
    expect(polarHostedUrl(value)).toBeUndefined();
    expect(navigateToPolarHostedPage(value, navigate)).toBe(false);
    expect(navigate).not.toHaveBeenCalled();
  });
});
