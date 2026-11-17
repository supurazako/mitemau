import { useRef, useEffect, useState } from "react";
import type { RelaySession } from "./lib/moq";
import { connectRelay } from "./lib/moq";

export default function RelayConnection() {
  const clientRef = useRef<RelaySession | null>(null);
  const [status, setStatus] = useState("Disconnected");

  useEffect(() => {
    return () => {
      const client = clientRef.current;
      clientRef.current = null;
      void client?.disconnect();
    };
  }, []);

  async function connect() {
    setStatus("Connecting");

    try {
      let ended = false;

      const session = connectRelay(
        "https://relay-test.supurazako.com:4433",
        () => {
          ended = true;
          clientRef.current = null;
          setStatus("Disconnected");
        },
      );

      clientRef.current = session;
      await session.ready;

      if (ended) return;

      setStatus("Connected");
    } catch (error) {
      console.error(error);
      setStatus("Failed");
    }
  }

  async function disconnect() {
    await clientRef.current?.disconnect();
    clientRef.current = null;
    setStatus("Disconnected");
  }

  return (
    <section>
      <h2>Relay connection</h2>
      <p role="status">{status}</p>
      <button
        type="button"
        disabled={status === "Connecting" || status === "Connected"}
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
  );
}
