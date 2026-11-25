import { createPublishingSession } from "./lib/publishingSession";
import CameraPreview from "./CameraPreview";
import { useRef, useEffect, useState } from "react";
import type { RelaySession } from "./lib/moq";
import { connectRelay, TEST_RELAY_URL } from "./lib/moq";

export default function RelayConnection({ streamID }: { streamID: string }) {
  const registrationRef = useRef<ReturnType<typeof createPublishingSession> | null>(null);
  const [publishing, setPublishing] = useState<{ namespace: string; registration: ReturnType<typeof createPublishingSession> } | null>(null);
  const mountedRef = useRef(false);
  const connectionVersion = useRef(0);
  const clientRef = useRef<RelaySession | null>(null);
  const [connectedRelay, setConnectedRelay] = useState<RelaySession | null>(null);
  const [status, setStatus] = useState("Disconnected");

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      void registrationRef.current?.stop().catch(console.error);
      const client = clientRef.current;
      clientRef.current = null;
      void client?.disconnect().catch(console.error);
    };
  }, []);

  async function connect() {
    setStatus("Connecting");
    const version = ++connectionVersion.current;
    const registration = createPublishingSession(streamID);
    registrationRef.current = registration;
    let session: RelaySession | null = null;

    try {
      let ended = false;
      const prepared = await registration.prepare();
      if (!mountedRef.current || version !== connectionVersion.current) {
        await registration.stop();
        return;
      }

      session = connectRelay(
        TEST_RELAY_URL,
        () => {
          ended = true;
          if (version !== connectionVersion.current) return;
          clientRef.current = null;
          registrationRef.current = null;
          if (mountedRef.current) {
            setConnectedRelay(null);
            setStatus("Disconnected");
            setPublishing(null);
          }
          void registration.stop().catch(console.error);
        },
        prepared.token,
      );

      clientRef.current = session;
      await session.ready;

      if (ended || !mountedRef.current || version !== connectionVersion.current) {
        await session.disconnect();
        await registration.stop();
        return;
      }

      setPublishing({ namespace: prepared.namespace, registration });
      setConnectedRelay(session);
      setStatus("Connected");
    } catch (error) {
      console.error(error);
      await session?.disconnect().catch(console.error);
      await registration.stop().catch(console.error);
      if (mountedRef.current && version === connectionVersion.current) {
        clientRef.current = null;
        registrationRef.current = null;
        setConnectedRelay(null);
        setPublishing(null);
        setStatus("Failed");
      }
    }
  }

  async function disconnect() {
    const version = ++connectionVersion.current;
    setStatus("Disconnecting");
    const session = clientRef.current;
    clientRef.current = null;
    setConnectedRelay(null);
    setPublishing(null);
    const registration = registrationRef.current;
    registrationRef.current = null;
    // A stalled API request must not keep the sending connection open.
    void registration?.stop().catch((error) => {
      console.error("Could not revoke publishing", error);
    });
    try {
      await session?.disconnect();
    } catch (error) {
      console.error("Could not disconnect Relay", error);
    } finally {
      if (mountedRef.current && version === connectionVersion.current) setStatus("Disconnected");
    }
  }

  return (
    <>
      <section>
        <h2>Relay connection</h2>
        <p role="status">{status}</p>
        <button
          type="button"
          disabled={status === "Connecting" || status === "Connected" || status === "Disconnecting"}
          onClick={() => void connect()}
        >
          Connect
        </button>
        <button
          type="button"
          disabled={status !== "Connected"}
          onClick={() => void disconnect()}
        >
          Disconnect
        </button>
      </section>
      <CameraPreview
        key={connectedRelay ? "connected" : "offline"}
        relay={connectedRelay}
        publishing={publishing}
        onPublishingStopped={() => {
          if (registrationRef.current === publishing?.registration) void disconnect();
        }}
      />
    </>
  );
}
