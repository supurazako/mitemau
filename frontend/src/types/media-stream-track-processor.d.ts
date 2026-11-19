export {};

declare global {
  class MediaStreamTrackProcessor {
    constructor(options: { track: MediaStreamTrack; maxBufferSize?: number });

    readonly readable: ReadableStream<VideoFrame>;
  }
}
