import CameraPreview from "./CameraPreview";
import { useRef, useEffect, useState } from "react";
import type { RelaySession } from "./lib/moq";
import { connectRelay, TEST_RELAY_URL } from "./lib/moq";

export default function RelayConnection({ namespacePrefix }: { namespacePrefix: string }) {
  const clientRef = useRef<RelaySession | null>(null);
  const [connectedRelay, setConnectedRelay] = useState<RelaySession | null>(null);
  const [status, setStatus] = useState("Disconnected");

  useEffect(() => {
    return () => {
      const client = clientRef.current;
      clientRef.current = null;
      void client?.disconnect().catch(console.error);
    };
  }, []);

  async function connect() {
    setStatus("Connecting");

    try {
      let ended = false;

      const session = connectRelay(
        TEST_RELAY_URL,
        () => {
          ended = true;
          clientRef.current = null;
          setConnectedRelay(null);
          setStatus("Disconnected");
        },
      );

      clientRef.current = session;
      await session.ready;

      if (ended) return;

      setConnectedRelay(session);
      setStatus("Connected");
    } catch (error) {
      console.error(error);
      setStatus("Failed");
    }
  }

  async function disconnect() {
    setStatus("Disconnecting");
    const session = clientRef.current;
    clientRef.current = null;
    setConnectedRelay(null);
    try {
      await session?.disconnect();
      setStatus("Disconnected");
    } catch (error) {
      console.error("Relay disconnect failed", error);
      setStatus("Failed");
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
        namespacePrefix={namespacePrefix}
      />
    </>
  );
}
