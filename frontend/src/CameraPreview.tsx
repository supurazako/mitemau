import { useRef, useState, useEffect } from "react";
import { startCamera, stopCamera } from "./lib/camera";
import { startVideoEncoding } from "./lib/videoEncoding";
import type { VideoEncodingSession } from "./lib/videoEncoding";

export default function CameraPreview() {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const mountedRef = useRef(false);
  const encodingRef = useRef<VideoEncodingSession | null>(null);

  const [status, setStatus] = useState("Stopped");

  useEffect(() => {
    mountedRef.current = true;

    return () => {
      mountedRef.current = false;

      const session = encodingRef.current;
      encodingRef.current = null;
      void session?.stop().catch(console.error);

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
      const session = startVideoEncoding(stream);
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

      if (mountedRef.current) setStatus("Stopped");
    }
  }

  return (
    <section>
      <h2>Camera preview</h2>
      <p role="status">{status}</p>
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
