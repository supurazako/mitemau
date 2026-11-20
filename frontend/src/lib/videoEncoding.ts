import { createVideoEncoder, encodeVideoFrames } from "./videoEncoder";
import { createVideoFrameReader } from "./videoFrames";

export type VideoEncodingSession = {
  done: Promise<void>;
  stop: () => Promise<void>;
};

export function startVideoEncoding(
  stream: MediaStream,
  onOutput: EncodedVideoChunkOutputCallback,
): VideoEncodingSession {
  const encoder = createVideoEncoder(onOutput);

  let reader: ReadableStreamDefaultReader<VideoFrame>;

  try {
    reader = createVideoFrameReader(stream);
  } catch (error) {
    encoder.close();
    throw error;
  }

  let cancelTask: Promise<void> | null = null;

  function cancelReading(): Promise<void> {
    cancelTask ??= reader.cancel();
    return cancelTask;
  }

  const done = (async () => {
    try {
      await encodeVideoFrames(reader, encoder);
      await encoder.flush();
    } finally {
      try {
        await cancelReading();
      } finally {
        reader.releaseLock();

        if (encoder.state !== "closed") {
          encoder.close();
        }
      }
    }
  })();

  return {
    done,
    async stop() {
      await cancelReading();
      await done;
    },
  };
}
