import { FullTrackName } from "moqtail";
import { useRef, useState, useEffect } from "react";
import { startCamera, stopCamera } from "./lib/camera";
import { startVideoEncoding } from "./lib/videoEncoding";
import type { RelaySession, VideoPublication } from "./lib/moq";
import type { VideoEncodingSession } from "./lib/videoEncoding";
import { createVideoObjectBuilder } from "./lib/videoObjects";

const previewTrack = FullTrackName.tryNew("mitemau/preview", "camera");

export default function CameraPreview({ relay }: { relay: RelaySession | null }) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const mountedRef = useRef(false);
  const encodingRef = useRef<VideoEncodingSession | null>(null);

  const publicationRef = useRef<VideoPublication | null>(null);
  const [trackNamespace, setTrackNamespace] = useState<string | null>(null);

  const [status, setStatus] = useState("Stopped");

  useEffect(() => {
    mountedRef.current = true;

    return () => {
      mountedRef.current = false;

      const session = encodingRef.current;
      encodingRef.current = null;
      void session?.stop().catch(console.error);

      const publication = publicationRef.current;
      publicationRef.current = null;
      void publication?.stop().catch(console.error);

      if (streamRef.current) {
        stopCamera(streamRef.current);
        streamRef.current = null;
      }
    };
  }, []);

  async function start() {
    if (status !== "Stopped") return;
    setStatus("Starting");
    try {
      const stream = await startCamera();
      if (!mountedRef.current) {
        stopCamera(stream);
        return;
      }

      streamRef.current = stream;

      if (videoRef.current) {
        videoRef.current.srcObject = stream;
      }
      const namespace = relay ? `mitemau/preview/${crypto.randomUUID()}` : null;
      const track = namespace
        ? FullTrackName.tryNew(namespace, "camera")
        : previewTrack;
      const publication = relay?.publishVideo(track);
      if (publication) {
        publicationRef.current = publication;
        await publication.ready;
        if (!mountedRef.current) return;
        setTrackNamespace(namespace);
      }

      const buildObject = createVideoObjectBuilder(track);
      const session = startVideoEncoding(stream, (chunk, metadata) => {
        const object = buildObject(chunk, metadata);
        if (object) {
          publication?.write(object);
          console.debug("Camera MoQ Object", {
            groupId: object.groupId,
            objectId: object.objectId,
            type: chunk.type,
            bytes: object.payload?.byteLength,
          });
        }
      });
      encodingRef.current = session;

      void session.done.catch((error) => {
        console.error("Encoding failed", error);

        if (encodingRef.current === session) {
          void stop();
        }
      });
      setStatus("Running");
    } catch (error) {
      console.error(error);
      await stop();
    }
  }

  async function stop() {
    if (mountedRef.current) setStatus("Stopping");

    const session = encodingRef.current;
    encodingRef.current = null;

    try {
      await session?.stop();
    } catch (error) {
      console.error(error);
    } finally {
      if (streamRef.current) {
        stopCamera(streamRef.current);
        streamRef.current = null;
      }

      if (videoRef.current) {
        videoRef.current.srcObject = null;
      }

      const publication = publicationRef.current;
      publicationRef.current = null;
      try {
        await publication?.stop();
      } catch (error) {
        console.error("Could not stop camera publication", error);
      }

      if (mountedRef.current) {
        setTrackNamespace(null);
        setStatus("Stopped");
      }
    }
  }

  return (
    <section>
      <h2>Camera preview</h2>
      <p role="status">{status}</p>
      {trackNamespace && <p>Publishing camera to {trackNamespace}</p>}
      <video ref={videoRef} autoPlay muted playsInline />
      <button
        type="button"
        disabled={status !== "Stopped"}
        onClick={() => void start()}
      >
        Start camera
      </button>
      <button
        type="button"
        disabled={status !== "Running"}
        onClick={() => void stop()}
      >
        Stop camera
      </button>
    </section>
  );
}
