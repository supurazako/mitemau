import { LOCProperties, Timestamp, VideoConfig, VideoFrameMarking } from "moqtail";
import type { MoqtObject } from "moqtail";
import type { PlaybackTimeline } from "./mediaTiming";
import { CAMERA_VIDEO_CODEC } from "./videoEncoder";

export function createCameraDecoder(
  canvas: HTMLCanvasElement,
  timeline: PlaybackTimeline,
  onFrame: () => void,
  onError: (error: DOMException) => void,
) {
  const drawingContext = canvas.getContext("2d");
  if (!drawingContext) throw new Error("Could not initialize the canvas");

  const context = drawingContext;
  const frames: VideoFrame[] = [];
  let animation: number | undefined;
  let finishDrain: (() => void) | undefined;
  let drainTimer: number | undefined;

  function render() {
    animation = undefined;
    let due: VideoFrame | undefined;
    while (frames.length > 0) {
      const time = timeline.presentationTime(frames[0].timestamp);
      if (time === undefined || time > timeline.now()) break;
      due?.close();
      due = frames.shift();
    }

    if (due) {
      try {
        if (canvas.width !== due.displayWidth) canvas.width = due.displayWidth;
        if (canvas.height !== due.displayHeight) canvas.height = due.displayHeight;
        context.drawImage(due, 0, 0);
        onFrame();
      } finally {
        due.close();
      }
    }

    if (frames.length > 0) animation = requestAnimationFrame(render);
    else finishDrain?.();
  }

  const decoder = new VideoDecoder({
    output(frame) {
      frames.push(frame);
      // Bound the queue while waiting for an audio anchor or a visible tab.
      if (frames.length > 12) frames.shift()?.close();
      animation ??= requestAnimationFrame(render);
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
    async drain() {
      if (decoder.state === "configured") await decoder.flush();
      const last = frames.at(-1);
      if (!last) return;
      const time = timeline.presentationTime(last.timestamp);
      if (time === undefined) return;
      await new Promise<void>((resolve) => {
        finishDrain = resolve;
        // A background tab may not get animation callbacks.
        drainTimer = window.setTimeout(resolve, Math.max(0, time - timeline.now()) * 1000 + 50);
      });
      window.clearTimeout(drainTimer);
    },
    close() {
      finishDrain?.();
      window.clearTimeout(drainTimer);
      if (animation !== undefined) cancelAnimationFrame(animation);
      for (const frame of frames.splice(0)) frame.close();
      if (decoder.state !== "closed") decoder.close();
    },
  };
}
