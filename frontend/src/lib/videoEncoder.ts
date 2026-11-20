export const CAMERA_VIDEO_CODEC = "avc1.42E01F";

export function createVideoEncoder(
  onOutput: EncodedVideoChunkOutputCallback,
): VideoEncoder {
  const encoder = new VideoEncoder({
    output: onOutput,
    error(error) {
      console.error("Video encoding failed", error);
    },
  });

  encoder.configure({
    codec: CAMERA_VIDEO_CODEC,
    width: 640,
    height: 360,
    bitrate: 300_000,
    framerate: 25,
    latencyMode: "realtime",
    avc: { format: "avc" },
  });

  return encoder;
}

export async function encodeVideoFrames(
  reader: ReadableStreamDefaultReader<VideoFrame>,
  encoder: VideoEncoder,
  onCapture: (timestamp: number) => void,
): Promise<void> {
  let frameCount = 0;

  while (true) {
    const { value: frame, done } = await reader.read();

    if (done) return;

    try {
      if (encoder.encodeQueueSize >= 2) continue;

      onCapture(frame.timestamp);
      encoder.encode(frame, {
        keyFrame: frameCount % 50 === 0,
      });

      frameCount++;
    } finally {
      frame.close();
    }
  }
}
