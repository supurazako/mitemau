export function createVideoFrameReader(
  stream: MediaStream,
): ReadableStreamDefaultReader<VideoFrame> {
  const track = stream.getVideoTracks()[0];

  if (!track) {
    throw new Error("No video track available");
  }

  const processor = new MediaStreamTrackProcessor({
    track,
    maxBufferSize: 1,
  });

  return processor.readable.getReader();
}
