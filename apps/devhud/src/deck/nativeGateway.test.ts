import { create, toBinary } from "@bufbuild/protobuf";
import {
  ErrorDetailSchema,
  ErrorReason,
} from "@delinoio/devhud-deck-connect";
import { describe, expect, it, vi } from "vitest";

import { DeckFailureCode, DeckProductError } from "./contracts";
import { mapFailure, NativeDeckGateway } from "./nativeGateway";
import { DeckProcedure, invokeDeckProcedure } from "./transport";

vi.mock("./transport", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./transport")>();
  return { ...actual, invokeDeckProcedure: vi.fn() };
});

function base64(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes));
}

describe("native Deck gateway", () => {
  it("shares one preflight, confirmation, and dispatch across concurrent manual refreshes", async () => {
    let resolveConfirmation: ((confirmed: boolean) => void) | undefined;
    const confirm = vi.fn(() => new Promise<boolean>((resolve) => {
      resolveConfirmation = resolve;
    }));
    vi.mocked(invokeDeckProcedure).mockImplementation(async (procedure) => {
      if (procedure === DeckProcedure.GetRefreshPreflight) {
        return { providerRefreshPrice: { value: 50n }, preflightToken: "token" } as never;
      }
      if (procedure === DeckProcedure.RefreshView) return {} as never;
      throw new Error(`Unexpected procedure: ${procedure}`);
    });
    const gateway = new NativeDeckGateway();

    const first = gateway.refreshView("view", confirm);
    const second = gateway.refreshView("view", confirm);

    expect(second).toBe(first);
    await vi.waitFor(() => expect(confirm).toHaveBeenCalledTimes(1));
    resolveConfirmation?.(true);
    await Promise.all([first, second]);
    expect(vi.mocked(invokeDeckProcedure).mock.calls.filter(
      ([procedure]) => procedure === DeckProcedure.GetRefreshPreflight,
    )).toHaveLength(1);
    expect(vi.mocked(invokeDeckProcedure).mock.calls.filter(
      ([procedure]) => procedure === DeckProcedure.RefreshView,
    )).toHaveLength(1);
  });

  it("maps typed Connect details and retry-after into the stable product error", () => {
    const detail = create(ErrorDetailSchema, {
      reason: ErrorReason.PROVIDER_RATE_LIMITED,
      retryAfter: { seconds: 30n, nanos: 1 },
    });
    let thrown: unknown;

    try {
      mapFailure({
        code: "rate-limited",
        detailBodyBase64: base64(toBinary(ErrorDetailSchema, detail)),
      });
    } catch (error) {
      thrown = error;
    }

    expect(thrown).toBeInstanceOf(DeckProductError);
    expect(thrown).toMatchObject({
      code: DeckFailureCode.ProviderRateLimited,
      retryAfterSeconds: 31,
    });
  });
});
