import { useRef, useState, useEffect } from "react";
import { startCamera, stopCamera } from "./lib/camera";

export default function CameraPreview() {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const mountedRef = useRef(false);

  const [status, setStatus] = useState("Stopped");

  useEffect(() => {
    mountedRef.current = true;

    return () => {
      mountedRef.current = false;

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
      setStatus("Running");
    } catch (error) {
      console.error(error);
      if (mountedRef.current) {
        setStatus("Stopped");
      }
    }
  }

  function stop() {
    if (streamRef.current) {
      stopCamera(streamRef.current);
      streamRef.current = null;
    }

    if (videoRef.current) {
      videoRef.current.srcObject = null;
    }
    setStatus("Stopped");
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
