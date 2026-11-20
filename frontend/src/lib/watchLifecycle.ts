type TrackKind = "audio" | "video";

export function createWatchLifecycle(
  cleanup: () => void,
  setStatus: (status: string) => void,
  onFinished: () => void,
) {
  let stopped = false;
  const ended = new Set<TrackKind>();

  function stop() {
    if (stopped) return;
    stopped = true;
    cleanup();
  }

  function finish(status: string) {
    if (stopped) return;
    stop();
    onFinished();
    setStatus(status);
  }

  return {
    get stopped() { return stopped; },
    stop,
    finish,
    frameDisplayed() {
      if (!stopped && ended.size === 0) setStatus("Playing");
    },
    trackEnded(track: TrackKind) {
      if (stopped || ended.has(track)) return;
      ended.add(track);
      if (ended.size === 2) {
        finish("Stream ended");
      } else {
        setStatus(track === "audio" ? "Audio ended" : "Video ended");
      }
    },
  };
}
