import LiveComments from "./LiveComments";
import { useEffect, useRef, useState } from "react";
import SharedStreamViewer from "./SharedStreamViewer";
import StreamInvitations from "./StreamInvitations";
import RelayConnection from "./RelayConnection";

type Stream = { id: string; creatorId: string; publisherId: string; title: string; state: "ready" | "live" | "ended"; endReason?: string };

const stateLabel: Record<Stream["state"], string> = { ready: "Ready to start", live: "Live", ended: "Ended" };

async function request<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, body === undefined ? {} : {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || "Stream request failed.");
  return result;
}

export default function StreamManager({ invitationsPage, invitee }: { invitationsPage: boolean; invitee?: string }) {
  const [access, setAccess] = useState<{ status: string; reason?: string | null }>();
  const canPublish = access?.status === "approved";
  const [streams, setStreams] = useState<Stream[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [title, setTitle] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ id: string; title: string } | null>(null);
  const working = useRef(false);
  const generation = useRef(0);
  const selected = streams.find((stream) => stream.id === selectedID);
  const isCreator = selected && selected.creatorId === selected.publisherId;

  useEffect(() => {
    let active = true;
    let refreshing = false;
    const refresh = async () => {
      if (working.current || refreshing) return;
      refreshing = true;
      const version = generation.current;
      try {
        const [result, permission] = await Promise.all([request<Stream[]>("/api/account/streams"), request<{ status: string; reason?: string | null }>("/api/account/broadcaster")]);
        if (active && version === generation.current) { setStreams(result); setAccess(permission); }
      } catch (cause) {
        if (active && version === generation.current) setError(String(cause));
      } finally {
        refreshing = false;
      }
    };
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);

  async function action(operation: "create" | "start" | "end" | "leave") {
    if (working.current) return;
    working.current = true;
    generation.current++;
    setBusy(true);
    setError("");
    try {
      let updated: Stream;
      if (operation === "create") {
        pending.current ??= { id: crypto.randomUUID().replaceAll("-", ""), title: title.trim() };
        updated = await request<Stream>("/api/streams", pending.current);
        pending.current = null;
        setTitle("");
      } else {
        if (!selected) return;
        if (operation === "leave") {
          await request(`/api/streams/${selected.id}/leave`, {});
          setStreams((items) => items.filter((item) => item.id !== selected.id));
          setSelectedID("");
          return;
        }
        updated = await request<Stream>(`/api/streams/${selected.id}/${operation}`, {});
      }
      setStreams((items) => [updated, ...items.filter((item) => item.id !== updated.id)]);
      setSelectedID(updated.id);
    } catch (cause) {
      setError(String(cause));
    } finally {
      generation.current++;
      working.current = false;
      setBusy(false);
    }
  }

  return (
    <>
    <section hidden={invitationsPage} className="stream-studio" aria-labelledby="stream-studio-heading">
      <header className="page-heading">
        <h2 id="stream-studio-heading">Broadcast studio</h2>

      </header>
      {access?.status === "suspended" && <p role="status">Broadcasting suspended: {access.reason}</p>}
      {access && !canPublish && access.status !== "suspended" &&
        <p role="status">You need broadcaster approval to create a stream or send media.</p>}
      {selectedID && !selected && <p role="status">This stream has ended or your participation has ended.</p>}
      {error && <p role="alert">{error}</p>}
      <div className="studio-setup">
        <form className="studio-panel" onSubmit={(event) => { event.preventDefault(); void action("create"); }}>
          <h3>Create a stream</h3>
          <label>Stream title <input value={title} maxLength={200} required disabled={busy}
            onChange={(event) => { pending.current = null; setTitle(event.target.value); }} /></label>
          <button className="primary-action" disabled={busy || !canPublish || !title.trim()}>Create stream</button>
        </form>
        <div className="studio-panel">
          <h3>Open a stream</h3>
          <label>Your streams <select value={selectedID} disabled={busy} onChange={(event) => setSelectedID(event.target.value)}>
            <option value="">Select a stream</option>
            {streams.map((stream) => <option key={stream.id} value={stream.id}>{stream.title} ({stateLabel[stream.state]})</option>)}
          </select></label>
        </div>
      </div>
      {selected && (
        <section className="studio-current">
          <header className="studio-panel">
            <p className={selected.state === "live" ? "live-badge" : "studio-state"} role="status">{stateLabel[selected.state]}</p>
            <h3>{selected.title}</h3>
            <p>{isCreator ? "You created this stream." : "You joined as a participant."}</p>
            {selected.endReason && <p role="status">Stream ended by staff: {selected.endReason}</p>}
            {isCreator && selected.state === "ready" &&
              <button className="primary-action" disabled={busy || !canPublish} onClick={() => void action("start")}>Start stream</button>}
            {selected.state !== "ended" && <div className="studio-exit">
              {isCreator
                ? <button className="danger-action" disabled={busy} onClick={() => {
                  if (window.confirm("End this stream for everyone? This stream cannot be restarted.")) void action("end");
                }}>End stream for everyone</button>
                : <button disabled={busy} onClick={() => void action("leave")}>Leave stream</button>}
            </div>}
          </header>
          {selected.state === "live" && <div className="watch-layout">
            <div className="watch-media">
              {canPublish && <RelayConnection key={`publish-${selected.id}`} streamID={selected.id} />}
              <SharedStreamViewer key={`view-${selected.id}`} streamID={selected.id} ownAccountID={selected.publisherId} />
            </div>
            <LiveComments key={`comments-${selected.id}`} streamID={selected.id} />
          </div>}
        </section>
      )}
    </section>
    <div hidden={!invitationsPage}>
      <StreamInvitations key={invitee ?? ""} invitee={invitee}
        streams={canPublish ? streams.filter(stream => stream.creatorId === stream.publisherId && stream.state !== "ended") : []}
        onChanged={() => { generation.current++; }} />
    </div>
    </>
  );
}
