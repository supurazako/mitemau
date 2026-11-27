import ScreenViewer from "./ScreenViewer";
import type { RelaySession } from "./lib/moq";
import type { PlaybackTimeline } from "./lib/mediaTiming";
import { FullTrackName, LOCProperties, VideoConfig, VideoFrameMarking } from "moqtail";
import { useEffect, useRef, useState } from "react";
import { connectRelay, TEST_RELAY_URL } from "./lib/moq";
import { createAudioPlayback } from "./lib/audioPlayback";
import { createPlaybackTimeline } from "./lib/mediaTiming";
import { createWatchLifecycle } from "./lib/watchLifecycle";
import { createCameraDecoder } from "./lib/videoDecoder";

export default function CameraViewer({ initialNamespace = "" }: { initialNamespace?: string } = {}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const stopRef = useRef<(() => void) | null>(null);
  const [namespace, setNamespace] = useState(initialNamespace);
  const [status, setStatus] = useState("Stopped");
  const [screenSession, setScreenSession] = useState<{
    relay: RelaySession;
    namespace: string;
    timeline: PlaybackTimeline;
  } | null>(null);
  const [watching, setWatching] = useState(false);

  useEffect(() => {
    function leavePage() {
      stopRef.current?.();
      setScreenSession(null);
      setWatching(false);
      setStatus("Stopped");
    }
    window.addEventListener("pagehide", leavePage);
    return () => {
      window.removeEventListener("pagehide", leavePage);
      stopRef.current?.();
    };
  }, []);

  async function watch() {
    const canvas = canvasRef.current;
    if (!canvas || stopRef.current || !namespace.trim()) return;

    setWatching(true);
    setStatus("Connecting");
    let cleanup: (() => void) | undefined;
    const lifecycle = createWatchLifecycle(
      () => {
        stopRef.current = null;
        cleanup?.();
      },
      setStatus,
      () => { setWatching(false); setScreenSession(null); },
    );
    stopRef.current = lifecycle.stop;

    try {
      const name = FullTrackName.tryNew(namespace.trim(), "camera");
      const context = new AudioContext();
      const timeline = createPlaybackTimeline(context);
      cleanup = () => { void context.close().catch(console.error); };
      await context.resume();
      if (lifecycle.stopped) return;
      const relay = connectRelay(TEST_RELAY_URL, () => lifecycle.finish("Disconnected"));
      let decoder: ReturnType<typeof createCameraDecoder> | undefined;
      let audio: ReturnType<typeof createAudioPlayback> | undefined;
      cleanup = () => {
        decoder?.close();
        audio?.stop();
        void context.close().catch(console.error);
        // This viewer owns the connection, so closing it releases every track.
        void relay.disconnect().catch(console.error);
      };
      await relay.ready;
      if (lifecycle.stopped) return;
      setScreenSession({ relay, namespace: namespace.trim(), timeline });
      console.info("[camera watch] subscribing", { namespace: namespace.trim() });
      const subscription = await relay.subscribeMedia(name);
      console.info("[camera watch] subscribed", { namespace: namespace.trim() });
      if (lifecycle.stopped) return;

      const audioName = FullTrackName.tryNew(namespace.trim(), "audio");
      void (async () => {
        const subscription = await relay.subscribeMedia(audioName);
        if (lifecycle.stopped) return;
        audio = createAudioPlayback(context, timeline, (error) => {
          console.error("Microphone playback failed", error);
          lifecycle.finish("Could not play audio");
        });
        await audio.play(subscription.stream);
        lifecycle.trackEnded("audio");
      })().catch((error) => {
        console.error("Microphone subscription failed", error);
        lifecycle.finish("Could not play audio");
      });
      setStatus("Waiting for a keyframe");
      decoder = createCameraDecoder(
        canvas,
        timeline,
        lifecycle.frameDisplayed,
        (error) => {
          console.error("Camera decode failed", error);
          lifecycle.finish("Decoding failed");
        },
        () => { if (!lifecycle.stopped) setStatus("Waiting for audio timing"); },
      );
      let received = 0;
      const reader = subscription.stream.getReader();
      try {
        while (!lifecycle.stopped) {
          const { value, done } = await reader.read();
          if (lifecycle.stopped) return;
          if (done) {
            break;
          }
          received++;
          if (received === 1 || value.objectId === 0n) {
            const properties = value.properties ? LOCProperties.fromKeyValuePairs(value.properties) : [];
            console.info("[camera watch] object received", {
              groupId: String(value.groupId), objectId: String(value.objectId),
              keyframe: properties.some((p) => p instanceof VideoFrameMarking && p.independent),
              hasConfig: properties.some((p) => p instanceof VideoConfig),
              bytes: value.payload?.byteLength,
            });
          }
          decoder.push(value);
        }
      } finally {
        reader.releaseLock();
      }

      if (lifecycle.stopped) return;
      await decoder.drain();
      lifecycle.trackEnded("video");
    } catch (error) {
      console.error("Camera subscription failed", error);
      lifecycle.finish("Could not play stream");
    }
  }

  return (
    <section>
      {initialNamespace ? <h3>Camera</h3> : <h2>Watch camera</h2>}
      <form onSubmit={(event) => {
        event.preventDefault();
        void watch();
      }}>
        {!initialNamespace && <label>
          Track namespace
          <input value={namespace} onChange={(event) => setNamespace(event.target.value)}
            disabled={watching} required />
        </label>}
        <button className="primary-action" disabled={watching}>Watch camera</button>
        <button type="button" disabled={!watching} onClick={() => {
          stopRef.current?.();
          setScreenSession(null);
          setWatching(false);
          setStatus("Stopped");
        }}>Stop watching</button>
      </form>
      <p role="status">{status}</p>
      <canvas ref={canvasRef} aria-label="Remote camera video" />
      {screenSession && <ScreenViewer {...screenSession} />}
    </section>
  );
}
