import { LOCProperties, Timestamp, VideoConfig, VideoFrameMarking } from "moqtail";
import type { MoqtObject } from "moqtail";
import { CAMERA_VIDEO_CODEC } from "./videoEncoder";

export function createCameraDecoder(
  canvas: HTMLCanvasElement,
  onFrame: () => void,
  onError: (error: DOMException) => void,
) {
  const context = canvas.getContext("2d");
  if (!context) throw new Error("Could not initialize the canvas");

  const decoder = new VideoDecoder({
    output(frame) {
      try {
        if (canvas.width !== frame.displayWidth) canvas.width = frame.displayWidth;
        if (canvas.height !== frame.displayHeight) canvas.height = frame.displayHeight;
        context.drawImage(frame, 0, 0);
        onFrame();
      } finally {
        frame.close();
      }
    },
    error: onError,
  });

  let groupId: bigint | undefined;
  let nextObjectId = 0n;

  return {
    push(object: MoqtObject) {
      if (!object.payload || !object.properties) return;
      const properties = LOCProperties.fromKeyValuePairs(object.properties);
      const timestamp = properties.find((property) => property instanceof Timestamp);
      const marking = properties.find((property) => property instanceof VideoFrameMarking);
      if (!timestamp || !marking) return;

      if (groupId !== undefined && object.groupId < groupId) return;
      if (marking.independent) {
        const config = properties.find((property) => property instanceof VideoConfig);
        if (!config) return;
        decoder.configure({
          codec: CAMERA_VIDEO_CODEC,
          description: new Uint8Array(config.config),
          optimizeForLatency: true,
        });
        groupId = object.groupId;
      } else if (object.groupId !== groupId || object.objectId !== nextObjectId) {
        return;
      }

      decoder.decode(new EncodedVideoChunk({
        type: marking.independent ? "key" : "delta",
        timestamp: Number(timestamp.timestamp),
        data: new Uint8Array(object.payload),
      }));
      nextObjectId = object.objectId + 1n;
    },
    flush: () => decoder.state === "configured" ? decoder.flush() : Promise.resolve(),
    close() {
      if (decoder.state !== "closed") decoder.close();
    },
  };
}
