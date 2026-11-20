export {};

declare global {
  class MediaStreamTrackProcessor<Frame extends VideoFrame | AudioData = VideoFrame> {
    constructor(options: { track: MediaStreamTrack; maxBufferSize?: number });

    readonly readable: ReadableStream<Frame>;
  }
}
