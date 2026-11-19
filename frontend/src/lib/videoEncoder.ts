export function createVideoEncoder(): VideoEncoder {
  const encoder = new VideoEncoder({
    output(chunk) {
      console.log({
        type: chunk.type,
        bytes: chunk.byteLength,
        timestamp: chunk.timestamp,
      });
    },
    error(error) {
      console.error("Video encoding failed", error);
    },
  });

  encoder.configure({
    codec: "avc1.42E01F",
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
): Promise<void> {
  let frameCount = 0;

  while (true) {
    const { value: frame, done } = await reader.read();

    if (done) return;

    try {
      if (encoder.encodeQueueSize >= 2) continue;

      encoder.encode(frame, {
        keyFrame: frameCount % 50 === 0,
      });

      frameCount++;
    } finally {
      frame.close();
    }
  }
}
