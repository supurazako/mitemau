import {
  LOCProperties,
  Location,
  MoqtObject,
  ObjectForwardingPreference,
} from "moqtail";
import type { FullTrackName } from "moqtail";

export function createVideoObjectBuilder(track: FullTrackName) {
  let groupId = -1n;
  let objectId = 0n;
  let videoConfig: Uint8Array | undefined;

  return (
    chunk: EncodedVideoChunk,
    metadata?: EncodedVideoChunkMetadata,
  ): MoqtObject | undefined => {
    const description = metadata?.decoderConfig?.description;
    if (description) {
      videoConfig = ArrayBuffer.isView(description)
        ? new Uint8Array(
            description.buffer,
            description.byteOffset,
            description.byteLength,
          ).slice()
        : new Uint8Array(description).slice();
    }

    const isKey = chunk.type === "key";
    if (isKey && videoConfig) {
      groupId++;
      objectId = 0n;
    } else if (groupId < 0n) {
      // A viewer needs a keyframe and AVC configuration to start decoding.
      return;
    }

    const properties = new LOCProperties()
      .addTimestamp(chunk.timestamp)
      .addVideoFrameMarking(isKey);
    if (isKey && videoConfig) {
      properties.addVideoConfig(videoConfig);
    }

    const payload = new Uint8Array(chunk.byteLength);
    chunk.copyTo(payload);

    return MoqtObject.newWithPayload(
      track,
      new Location(groupId, objectId++),
      128,
      ObjectForwardingPreference.Subgroup,
      0n,
      properties.build(),
      payload,
    );
  };
}
