import { useEffect, useState } from "react";
import CameraViewer from "./CameraViewer";

type Participant = { accountId: string; namespace: string | null; publishingState: "stopped" | "publishing" | "disconnected" };

export default function SharedStreamViewer({ streamID, ownAccountID }: {
  streamID: string;
  ownAccountID: string;
}) {
  const [participants, setParticipants] = useState<Participant[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    let refreshing = false;
    async function refresh() {
      if (refreshing) return;
      refreshing = true;
      try {
        const response = await fetch(`/api/streams/${streamID}/participants`, { signal: controller.signal });
        if (!response.ok) throw new Error("Could not load participants.");
        const next: Participant[] = await response.json();
        if (!controller.signal.aborted) {
          setParticipants(next);
          setError("");
        }
      } catch (cause) {
        if (!controller.signal.aborted) setError(String(cause));
      } finally { refreshing = false; }
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [streamID]);

  return (
    <section className="studio-panel">
      <h3>Participants</h3>
      {error && <p role="alert">{error}</p>}
      {participants.map((participant) => (
        <section className="studio-participant" key={participant.accountId}>
          <p>{participant.accountId}{participant.accountId === ownAccountID ? " (you)" : ""}</p>
          {!participant.namespace && <p>{participant.publishingState === "disconnected" ? "Connection lost. Waiting for the participant to reconnect." : "Not publishing"}</p>}
          {participant.namespace && participant.accountId !== ownAccountID && (
            <CameraViewer key={participant.namespace} initialNamespace={participant.namespace} />
          )}
        </section>
      ))}
    </section>
  );
}
