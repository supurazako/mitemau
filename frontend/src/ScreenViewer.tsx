import { FullTrackName } from "moqtail";
import { useEffect, useRef, useState } from "react";
import type { RelaySession } from "./lib/moq";
import type { PlaybackTimeline } from "./lib/mediaTiming";
import { createCameraDecoder } from "./lib/videoDecoder";

export default function ScreenViewer({ relay, namespace, timeline }: {
  relay: RelaySession;
  namespace: string;
  timeline: PlaybackTimeline;
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const active = useRef<{ stop: () => void } | null>(null);
  const [status, setStatus] = useState("Screen not subscribed");

  useEffect(() => () => {
    active.current?.stop();
    active.current = null;
  }, []);

  async function watch() {
    const canvas = canvasRef.current;
    if (!canvas || active.current) return;
    setStatus("Connecting to screen");
    let stopped = false;
    let decoder: ReturnType<typeof createCameraDecoder> | undefined;
    let unsubscribe: (() => Promise<void>) | undefined;
    const session = {
      stop() {
        stopped = true;
        decoder?.close();
        void unsubscribe?.().catch(console.error);
      },
    };
    active.current = session;
    try {
      console.info("[screen watch] subscribing", { namespace });
      const subscription = await relay.subscribeMedia(FullTrackName.tryNew(namespace, "screen"));
      console.info("[screen watch] subscribed", { namespace });
      unsubscribe = subscription.unsubscribe;
      if (stopped) {
        await unsubscribe();
        return;
      }
      setStatus("Waiting for screen keyframe");
      decoder = createCameraDecoder(canvas, timeline, () => {
        if (!stopped) setStatus("Screen playing");
      }, (error) => {
        console.error(error);
        session.stop();
        if (active.current === session) {
          active.current = null;
          setStatus("Screen playback failed");
        }
      });
      let received = 0;
      const reader = subscription.stream.getReader();
      try {
        while (!stopped) {
          const {value, done} = await reader.read();
          if (stopped) return;
          if (done) {
            console.info("[screen watch] stream ended", { namespace, received });
            unsubscribe = undefined;
            break;
          }
          received++;
          if (received === 1 || value.objectId === 0n) console.info("[screen watch] object received", {
            namespace, groupId: String(value.groupId), objectId: String(value.objectId),
            bytes: value.payload?.byteLength,
          });
          decoder.push(value);
        }
      } finally {
        reader.releaseLock();
      }
      if (!stopped) {
        await decoder.drain();
        if (!stopped) setStatus("Screen ended");
      }
    } catch (error) {
      console.error(error);
      if (!stopped) setStatus("Could not watch screen");
    } finally {
      session.stop();
      if (active.current === session) active.current = null;
    }
  }

  return (
    <section aria-label="Shared screen">
      <h3>Shared screen</h3>
      <p role="status">{status}</p>
      <button className="primary-action" type="button" disabled={status === "Connecting to screen" || status === "Waiting for screen keyframe" || status === "Screen playing"}
        onClick={() => void watch()}>Watch shared screen</button>
      <canvas ref={canvasRef} aria-label="Remote shared screen" />
    </section>
  );
}
