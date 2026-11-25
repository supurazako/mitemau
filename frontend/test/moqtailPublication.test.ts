import { createRequire } from "node:module";
import { FullTrackName, PublishDoneStatusCode, Subscribe } from "moqtail";
import { SubscribePublication } from "moqtail/client";
import { expect, test, vi } from "vitest";

const commonJS = createRequire(import.meta.url)("moqtail/client") as typeof import("moqtail/client");

function publicationFixture(Publication: typeof SubscribePublication) {
  let onDone = () => {};
  const removeDone = vi.fn();
  const cancelObjects = vi.fn();
  const live = {
    onDone(callback: () => void) {
      onDone = callback;
      return removeDone;
    },
    onNewObject: vi.fn(() => cancelObjects),
  };
  const send = vi.fn(async () => {});
  type Arguments = ConstructorParameters<typeof SubscribePublication>;
  const client = { publications: new Map() };
  const track = {
    fullTrackName: FullTrackName.tryNew("test/public-viewer", "camera"),
    trackAlias: 1n,
    publisherPriority: 128,
    trackSource: { live },
  };
  const request = new Subscribe(0n, track.fullTrackName, []);
  const publication = new Publication(
    client as unknown as Arguments[0],
    track as unknown as Arguments[1],
    request,
    { send } as unknown as Arguments[3],
  );
  return { publication, send, removeDone, cancelObjects, complete: () => onDone() };
}

for (const [format, Publication] of [
  ["ESM", SubscribePublication],
  ["CommonJS", commonJS.SubscribePublication],
] as const) {
  test(`${format}: a cancelled publication removes its completion listener and ignores a queued completion`, async () => {
    const fixture = publicationFixture(Publication);
    fixture.publication.cancel();
    fixture.complete();
    await fixture.publication.done(PublishDoneStatusCode.TrackEnded);
    expect(fixture.removeDone).toHaveBeenCalledOnce();
    expect(fixture.send).not.toHaveBeenCalled();
  });

  test(`${format}: normal track completion sends its termination message once`, async () => {
    const fixture = publicationFixture(Publication);
    fixture.complete();
    fixture.complete();
    await fixture.publication.done(PublishDoneStatusCode.TrackEnded);
    expect(fixture.send).toHaveBeenCalledOnce();
    expect(fixture.removeDone).toHaveBeenCalledOnce();
  });

  test(`${format}: completion on a peer-closed request handles the rejection and cancels the publication`, async () => {
    const fixture = publicationFixture(Publication);
    fixture.send.mockRejectedValueOnce(new Error("Peer request already closed"));
    fixture.complete();
    await vi.waitFor(() => expect(fixture.cancelObjects).toHaveBeenCalledOnce());
  });
}
