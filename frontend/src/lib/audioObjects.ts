import { LOCProperties, Location, MoqtObject, ObjectForwardingPreference } from "moqtail";
import type { FullTrackName } from "moqtail";

export function createAudioObjectBuilder(track: FullTrackName) {
  let index = 0n;
  const packetsPerGroup = 50n;

  return (chunk: EncodedAudioChunk, timestamp: number): MoqtObject => {
    const payload = new Uint8Array(chunk.byteLength);
    chunk.copyTo(payload);
    const location = new Location(index / packetsPerGroup, index % packetsPerGroup);
    index++;

    return MoqtObject.newWithPayload(
      track,
      location,
      128,
      ObjectForwardingPreference.Subgroup,
      0n,
      new LOCProperties().addTimestamp(timestamp).build(),
      payload,
    );
  };
}
