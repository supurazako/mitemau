import { FullTrackName } from "moqtail";
import { useEffect, useRef, useState } from "react";
import type { MediaPublication } from "./lib/moq";
import { createCaptureTimeline } from "./lib/mediaTiming";
import { startVideoEncoding } from "./lib/videoEncoding";
import { createVideoObjectBuilder } from "./lib/videoObjects";

type ScreenShareProps = {
  namespace: string;
  publication: MediaPublication;
};

export default function ScreenShare({ namespace, publication }: ScreenShareProps) {
  const builder = useRef<{
    namespace: string;
    build: ReturnType<typeof createVideoObjectBuilder>;
  } | null>(null);
  const current = useRef<{ close: () => void } | null>(null);
  const [status, setStatus] = useState("Screen not shared");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => () => {
    const share = current.current;
    current.current = null;
    share?.close();
  }, []);

  function stop() {
    const share = current.current;
    current.current = null;
    share?.close();
    setStatus("Screen not shared");
  }

  async function start() {
    if (current.current) return;
    setError(null);
    setStatus("Choosing a screen");

    let stream: MediaStream | undefined;
    let encoding: ReturnType<typeof startVideoEncoding> | undefined;
    let source: ReturnType<MediaPublication["addTrack"]> | undefined;
    const share = {
      close() {
        console.info("[screen publish] stopped", { namespace });
        source?.stop();
        stream?.getTracks().forEach((track) => track.stop());
        void encoding?.stop().catch(console.error);
      },
    };
    current.current = share;

    try {
      stream = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: false });
      if (current.current !== share) {
        share.close();
        return;
      }

      const display = stream.getVideoTracks()[0];
      if (!display || display.readyState !== "live") {
        throw new Error("Screen capture ended before sharing started");
      }

      display.addEventListener("ended", () => {
        if (current.current === share) stop();
      }, { once: true });

      const name = FullTrackName.tryNew(namespace, "screen");
      source = publication.addTrack(name);
      console.info("[screen publish] track registered", { namespace });
      // Reusing a track name also reuses its location sequence at the relay.
      if (builder.current?.namespace !== namespace) {
        builder.current = { namespace, build: createVideoObjectBuilder(name) };
      }
      const buildObject = builder.current.build;
      const timeline = createCaptureTimeline();
      encoding = startVideoEncoding(stream, (chunk, metadata) => {
        if (current.current !== share) return;
        const object = buildObject(chunk, metadata, timeline.translate(chunk.timestamp));
        if (object) {
          publication.write(object);
          if (chunk.type === "key") console.info("[screen publish] keyframe queued", {
            namespace, groupId: String(object.groupId), bytes: object.payload?.byteLength,
          });
        }
      }, timeline.observe);
      setStatus("Sharing screen");

      await encoding.done;
      if (current.current === share) stop();
    } catch (cause) {
      if (current.current !== share) return;
      if (cause instanceof DOMException && cause.name === "NotAllowedError") {
        setError("Screen sharing was cancelled or permission was denied.");
      } else {
        console.error("Screen sharing failed", cause);
        setError("Could not share the screen.");
      }
      stop();
    }
  }

  return (
    <section aria-label="Screen sharing">
      <p role="status">{status}</p>
      {error && <p role="alert">{error}</p>}
      <button type="button" disabled={status !== "Screen not shared"} onClick={() => void start()}>
        Share screen
      </button>
      <button type="button" disabled={status !== "Sharing screen"} onClick={stop}>
        Stop sharing screen
      </button>
    </section>
  );
}
