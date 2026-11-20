import { expect, test } from "vitest";
import { createWatchLifecycle } from "../src/lib/watchLifecycle";

for (const [first, second, status] of [
  ["audio", "video", "Audio ended"],
  ["video", "audio", "Video ended"],
] as const) {
  test(`${first} ending first keeps the other track playing`, () => {
    const events: string[] = [];
    const watch = createWatchLifecycle(
      () => events.push("cleanup"),
      (status) => events.push(status),
      () => events.push("finished"),
    );

    watch.frameDisplayed();
    watch.trackEnded(first);
    watch.frameDisplayed();
    watch.trackEnded(first);
    expect(events).toEqual(["Playing", status]);
    expect(watch.stopped).toBe(false);

    watch.trackEnded(second);
    expect(events).toEqual(["Playing", status, "cleanup", "finished", "Stream ended"]);
    expect(watch.stopped).toBe(true);
  });
}

test("viewer stop cleans up immediately and ignores late media callbacks", () => {
  const events: string[] = [];
  const watch = createWatchLifecycle(
    () => events.push("cleanup"),
    (status) => events.push(status),
    () => events.push("finished"),
  );

  watch.trackEnded("audio");
  watch.stop();
  expect(watch.stopped).toBe(true);
  watch.frameDisplayed();
  watch.trackEnded("video");
  watch.finish("Disconnected");
  watch.stop();
  expect(events).toEqual(["Audio ended", "cleanup"]);
});
