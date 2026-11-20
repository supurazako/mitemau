import { FullTrackName } from "moqtail";
import { useEffect, useRef, useState } from "react";
import { connectRelay, TEST_RELAY_URL } from "./lib/moq";
import { createCameraDecoder } from "./lib/videoDecoder";

export default function CameraViewer() {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const stopRef = useRef<(() => void) | null>(null);
  const [namespace, setNamespace] = useState("");
  const [status, setStatus] = useState("Stopped");
  const [watching, setWatching] = useState(false);

  useEffect(() => () => stopRef.current?.(), []);

  async function watch() {
    const canvas = canvasRef.current;
    if (!canvas || stopRef.current || !namespace.trim()) return;

    setWatching(true);
    setStatus("Connecting");
    let stopped = false;
    let cleanup: (() => void) | undefined;
    const finish = (status: string) => {
      if (stopped) return;
      stop();
      setWatching(false);
      setStatus(status);
    };
    function stop() {
      if (stopped) return;
      stopped = true;
      stopRef.current = null;
      cleanup?.();
    }
    stopRef.current = stop;

    try {
      const name = FullTrackName.tryNew(namespace.trim(), "camera");
      const relay = connectRelay(TEST_RELAY_URL, () => finish("Disconnected"));
      let decoder: ReturnType<typeof createCameraDecoder> | undefined;
      let unsubscribe: (() => Promise<void>) | undefined;
      cleanup = () => {
        decoder?.close();
        void (async () => {
          try {
            await unsubscribe?.();
          } finally {
            await relay.disconnect();
          }
        })().catch(console.error);
      };
      await relay.ready;
      if (stopped) return;
      const subscription = await relay.subscribeVideo(name);
      if (stopped) return;

      unsubscribe = subscription.unsubscribe;
      setStatus("Waiting for a keyframe");
      decoder = createCameraDecoder(
        canvas,
        () => setStatus("Playing"),
        (error) => {
          console.error("Camera decode failed", error);
          finish("Decoding failed");
        },
      );
      const reader = subscription.stream.getReader();
      try {
        while (!stopped) {
          const { value, done } = await reader.read();
          if (stopped) return;
          if (done) {
            unsubscribe = undefined;
            break;
          }
          decoder.push(value);
        }
      } finally {
        reader.releaseLock();
      }

      if (stopped) return;
      await decoder.flush();
      finish("Stream ended");
    } catch (error) {
      console.error("Camera subscription failed", error);
      finish("Could not play stream");
    }
  }

  return (
    <section>
      <h2>Watch camera</h2>
      <form onSubmit={(event) => {
        event.preventDefault();
        void watch();
      }}>
        <label>
          Track namespace
          <input value={namespace} onChange={(event) => setNamespace(event.target.value)}
            disabled={watching} required />
        </label>
        <button disabled={watching}>Watch</button>
        <button type="button" disabled={!watching} onClick={() => {
          stopRef.current?.();
          setWatching(false);
          setStatus("Stopped");
        }}>Stop watching</button>
      </form>
      <p role="status">{status}</p>
      <canvas ref={canvasRef} aria-label="Remote camera video" />
    </section>
  );
}
