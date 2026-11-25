import { afterEach, expect, test, vi } from "vitest";
import { createPublishingSession } from "../src/lib/publishingSession";

afterEach(() => vi.unstubAllGlobals());

test("preparation requires a Relay credential before a sending connection can open", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(
    JSON.stringify({ namespace: "mitemau/streams/test/publishers/test/session" }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  )));

  await expect(createPublishingSession("test").prepare())
    .rejects.toThrow("Publishing authorization missing.");
});
