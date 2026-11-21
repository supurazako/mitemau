import { FullTrackName, LOCProperties, VideoConfig } from "moqtail";
import { expect, test } from "vitest";
import { createVideoObjectBuilder } from "../src/lib/videoObjects";

function chunk(type: EncodedVideoChunkType): EncodedVideoChunk {
  return {
    type,
    byteLength: 1,
    copyTo(destination: Uint8Array) { destination[0] = 42; },
  } as EncodedVideoChunk;
}

test("a retained screen builder advances groups across encoder restarts", () => {
  const build = createVideoObjectBuilder(FullTrackName.tryNew("test/session", "screen"));
  const config = (byte: number) => ({
    decoderConfig: { codec: "avc1.42E01F", description: Uint8Array.of(byte) },
  });

  const first = build(chunk("key"), config(1), 100);
  const delta = build(chunk("delta"), undefined, 120);
  // A new encoder emits a new keyframe and configuration on sharing restart.
  const restarted = build(chunk("key"), config(2), 1000);
  const next = build(chunk("delta"), undefined, 1020);

  expect([first, delta, restarted, next].map((object) => [
    object?.groupId, object?.objectId,
  ])).toEqual([[0n, 0n], [0n, 1n], [1n, 0n], [1n, 1n]]);
  const properties = LOCProperties.fromKeyValuePairs(restarted!.properties!);
  expect(properties.find((property) => property instanceof VideoConfig)?.config)
    .toEqual(Uint8Array.of(2));
});
