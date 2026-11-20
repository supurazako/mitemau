export const MICROPHONE_CODEC = {
  codec: "opus",
  sampleRate: 48000,
  numberOfChannels: 1,
} as const;

export function startAudioEncoding(
  track: MediaStreamTrack,
  onOutput: EncodedAudioChunkOutputCallback,
  onCapture: (timestamp: number) => void,
) {
  const reader = new MediaStreamTrackProcessor<AudioData>({ track }).readable.getReader();
  let cancelTask: Promise<void> | undefined;
  const cancel = () => cancelTask ??= reader.cancel();
  let failure: DOMException | undefined;
  let encoder: AudioEncoder | undefined;

  const done = (async () => {
    try {
      encoder = new AudioEncoder({
        output: onOutput,
        error(error) {
          failure = error;
          void cancel().catch(console.error);
        },
      });
      const activeEncoder = encoder;
      activeEncoder.configure({ ...MICROPHONE_CODEC, bitrate: 32000 });
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        try {
          onCapture(value.timestamp);
          activeEncoder.encode(value);
        } finally {
          value.close();
        }
      }
      if (failure) throw failure;
      await activeEncoder.flush();
    } finally {
      try {
        await cancel();
      } finally {
        reader.releaseLock();
        if (encoder && encoder.state !== "closed") encoder.close();
      }
    }
  })();

  return { done, async stop() { await cancel(); await done; } };
}
