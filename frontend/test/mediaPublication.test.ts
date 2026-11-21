import { FullTrackName } from "moqtail";
import type { Track } from "moqtail/client";
import { beforeEach, expect, test, vi } from "vitest";
import { connectRelay } from "../src/lib/moq";

const relay = vi.hoisted(() => ({
  disconnect: vi.fn(async () => {}),
  init: vi.fn(async () => {}),
  on: vi.fn(),
  addOrUpdateTrack: vi.fn<(track: Track) => void>(),
  removeTrack: vi.fn<(track: Track) => void>(),
  publishNamespace: vi.fn(async () => ({})),
  unpublishNamespace: vi.fn(async () => {}),
}));

vi.mock("moqtail/client", async (importOriginal) => ({
  ...await importOriginal<typeof import("moqtail/client")>(),
  MOQtailClient: class { constructor() { return relay; } },
}));

beforeEach(() => vi.clearAllMocks());

test("stopping a dynamically added track leaves the namespace and other tracks live", async () => {
  const session = connectRelay("https://relay.example", () => {});
  await session.ready;
  const camera = FullTrackName.tryNew("test/session", "camera");
  const audio = FullTrackName.tryNew("test/session", "audio");
  const screen = FullTrackName.tryNew("test/session", "screen");
  const publication = session.publishMedia([camera, audio]);
  await publication.ready;
  const sharing = publication.addTrack(screen);
  const [cameraTrack, audioTrack, screenTrack] = relay.addOrUpdateTrack.mock.calls.map(([track]) => track);
  const [cameraEnded, audioEnded, screenEnded] = [cameraTrack, audioTrack, screenTrack].map((track) => {
    const ended = vi.fn();
    track.trackSource.live!.onDone(ended);
    return ended;
  });

  sharing.stop();
  await vi.waitFor(() => expect(screenEnded).toHaveBeenCalledOnce());
  expect(cameraEnded).not.toHaveBeenCalled();
  expect(audioEnded).not.toHaveBeenCalled();
  expect(relay.removeTrack.mock.calls).toEqual([[screenTrack]]);
  expect(relay.unpublishNamespace).not.toHaveBeenCalled();

  const restarted = publication.addTrack(FullTrackName.tryNew("test/session", "screen"));
  sharing.stop(); // A stale stop handle must not close the restarted source.
  const restartedTrack = relay.addOrUpdateTrack.mock.calls.at(-1)![0];
  const restartedEnded = vi.fn();
  restartedTrack.trackSource.live!.onDone(restartedEnded);
  expect(restartedEnded).not.toHaveBeenCalled();
  restarted.stop();
  await vi.waitFor(() => expect(restartedEnded).toHaveBeenCalledOnce());

  await publication.stop();
  await vi.waitFor(() => {
    expect(cameraEnded).toHaveBeenCalledOnce();
    expect(audioEnded).toHaveBeenCalledOnce();
  });
  expect(relay.unpublishNamespace).toHaveBeenCalledOnce();
  expect(relay.removeTrack.mock.calls).toEqual([[screenTrack], [restartedTrack], [cameraTrack], [audioTrack]]);
});


test("screen cleanup after disconnect never calls a destroyed client", async () => {
  const session = connectRelay("https://relay.example", () => {});
  await session.ready;
  const publication = session.publishMedia([FullTrackName.tryNew("test/disconnect", "camera")]);
  await publication.ready;
  const screen = publication.addTrack(FullTrackName.tryNew("test/disconnect", "screen"));
  await session.disconnect();
  relay.removeTrack.mockImplementation(() => { throw new Error("Client destroyed"); });
  relay.unpublishNamespace.mockImplementation(async () => { throw new Error("Client destroyed"); });
  try {
    expect(() => screen.stop()).not.toThrow();
    await expect(publication.stop()).resolves.toBeUndefined();
    expect(relay.removeTrack).not.toHaveBeenCalled();
    expect(relay.unpublishNamespace).not.toHaveBeenCalled();
  } finally {
    relay.removeTrack.mockReset();
    relay.unpublishNamespace.mockReset();
  }
});
