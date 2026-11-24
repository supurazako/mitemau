import { createPublishingSession } from "./lib/publishingSession";
import ScreenShare from "./ScreenShare";
import { FullTrackName } from "moqtail";
import { useRef, useState, useEffect } from "react";
import { startCamera, stopCamera } from "./lib/camera";
import { startVideoEncoding } from "./lib/videoEncoding";
import type { RelaySession, MediaPublication } from "./lib/moq";
import type { VideoEncodingSession } from "./lib/videoEncoding";
import { startAudioEncoding } from "./lib/audioEncoding";
import { createAudioObjectBuilder } from "./lib/audioObjects";
import { createCaptureTimeline } from "./lib/mediaTiming";
import { createVideoObjectBuilder } from "./lib/videoObjects";

const previewTrack = FullTrackName.tryNew("mitemau/preview", "camera");

export default function CameraPreview({ relay, streamID }: {
  relay: RelaySession | null;
  streamID: string;
}) {
  const registrationRef = useRef<ReturnType<typeof createPublishingSession> | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const mountedRef = useRef(false);
  const encodingRef = useRef<VideoEncodingSession | null>(null);

  const audioRef = useRef<ReturnType<typeof startAudioEncoding> | null>(null);
  const [captureError, setCaptureError] = useState<string | null>(null);
  const publicationRef = useRef<MediaPublication | null>(null);
  const [activePublication, setActivePublication] = useState<{
    namespace: string;
    publication: MediaPublication;
  } | null>(null);

  const [status, setStatus] = useState("Stopped");

  useEffect(() => {
    mountedRef.current = true;

    return () => {
      mountedRef.current = false;

      const session = encodingRef.current;
      encodingRef.current = null;
      void session?.stop().catch(console.error);

      const audio = audioRef.current;
      audioRef.current = null;
      void audio?.stop().catch(console.error);

      const registration = registrationRef.current;
      registrationRef.current = null;
      void registration?.stop().catch(console.error);

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
    setCaptureError(null);
    try {
      const stream = await startCamera(relay !== null);
      if (!mountedRef.current) {
        stopCamera(stream);
        return;
      }

      streamRef.current = stream;

      if (videoRef.current) {
        videoRef.current.srcObject = stream;
      }
      let namespace: string | null = null;
      if (relay) {
        const registration = createPublishingSession(streamID);
        registrationRef.current = registration;
        const result = await registration.prepare();
        if (!mountedRef.current) {
          await registration.stop();
          return;
        }
        namespace = result.namespace;
      }
      const track = namespace
        ? FullTrackName.tryNew(namespace, "camera")
        : previewTrack;
      const audioTrack = namespace && FullTrackName.tryNew(namespace, "audio");
      const publication = relay && audioTrack ? relay.publishMedia([track, audioTrack]) : null;
      if (publication && namespace) {
        publicationRef.current = publication;
        await publication.ready;
        if (!mountedRef.current) return;
        const registration = registrationRef.current;
        if (!registration) return;
        await registration.start();
        if (!mountedRef.current) {
          await registration.stop();
          return;
        }
        setActivePublication({ namespace, publication });
      }

      const videoTime = createCaptureTimeline();
      const buildObject = createVideoObjectBuilder(track);
      const session = startVideoEncoding(stream, (chunk, metadata) => {
        const object = buildObject(chunk, metadata, videoTime.translate(chunk.timestamp));
        if (object) {
          publication?.write(object);
          console.debug("Camera MoQ Object", {
            groupId: object.groupId,
            objectId: object.objectId,
            type: chunk.type,
            bytes: object.payload?.byteLength,
          });
        }
      }, videoTime.observe);
      encodingRef.current = session;

      if (publication && audioTrack) {
        const microphone = stream.getAudioTracks()[0];
        if (!microphone) throw new Error("Microphone track missing");
        const audioTime = createCaptureTimeline();
        const buildAudioObject = createAudioObjectBuilder(audioTrack);
        const audio = startAudioEncoding(microphone, (chunk) => {
          publication.write(buildAudioObject(chunk, audioTime.translate(chunk.timestamp)));
        }, audioTime.observe);
        audioRef.current = audio;
        void audio.done.catch((error) => {
          console.error("Microphone encoding failed", error);
          if (audioRef.current === audio) {
            setCaptureError("Microphone encoding failed.");
            void stop();
          }
        });
      }

      void session.done.catch((error) => {
        console.error("Encoding failed", error);

        if (encodingRef.current === session) {
          void stop();
        }
      });
      setStatus("Running");
    } catch (error) {
      console.error(error);
      if (mountedRef.current) {
        setCaptureError(error instanceof DOMException && error.name === "NotAllowedError"
          ? "Camera or microphone permission was denied."
          : "Could not start media capture or publishing.");
      }
      await stop();
    }
  }

  async function stop() {
    if (mountedRef.current) setStatus("Stopping");

    const session = encodingRef.current;
    encodingRef.current = null;

    const audio = audioRef.current;
    audioRef.current = null;
    try {
      const results = await Promise.allSettled([session?.stop(), audio?.stop()]);
      for (const result of results) {
        if (result.status === "rejected") console.error(result.reason);
      }
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

      const registration = registrationRef.current;
      registrationRef.current = null;
      try { await registration?.stop(); }
      catch (error) { console.error("Could not clear publication", error); }
      if (mountedRef.current) {
        setActivePublication(null);
        setStatus("Stopped");
      }
    }
  }

  return (
    <section>
      <h2>Camera preview</h2>
      <p role="status">{status}</p>
      {captureError && <p role="alert">{captureError}</p>}
      {activePublication && <p>Publishing camera to {activePublication.namespace}</p>}
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
      {status === "Running" && activePublication && (
        <ScreenShare namespace={activePublication.namespace} publication={activePublication.publication} />
      )}
    </section>
  );
}
