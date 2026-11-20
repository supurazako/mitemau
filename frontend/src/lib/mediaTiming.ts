export function createCaptureTimeline() {
  let offsetUs: number | undefined;
  return {
    observe(timestamp: number) {
      offsetUs ??= Math.round(performance.now() * 1000) - timestamp;
    },
    translate(timestamp: number) {
      return timestamp + (offsetUs ?? 0);
    },
  };
}

export function createPlaybackTimeline(context: AudioContext) {
  let anchor: { mediaUs: number; audioSeconds: number } | undefined;
  const bufferSeconds = 0.12;

  function presentationTime(timestamp: number) {
    if (!anchor) return undefined;
    return anchor.audioSeconds + (timestamp - anchor.mediaUs) / 1_000_000;
  }

  return {
    presentationTime,
    scheduleAudio(timestamp: number) {
      const scheduled = presentationTime(timestamp);
      if (scheduled !== undefined && scheduled >= context.currentTime) {
        return scheduled;
      }

      anchor = {
        mediaUs: timestamp,
        audioSeconds: context.currentTime + bufferSeconds,
      };
      return anchor.audioSeconds;
    },
    now: () => context.currentTime,
  };
}

export type PlaybackTimeline = ReturnType<typeof createPlaybackTimeline>;
