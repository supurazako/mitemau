import { useEffect, useRef, useState } from "react";
import SharedStreamViewer from "./SharedStreamViewer";
import StreamInvitations from "./StreamInvitations";
import RelayConnection from "./RelayConnection";

type Stream = { id: string; creatorId: string; publisherId: string; title: string; state: "ready" | "live" | "ended" };

async function request<T>(path: string, body?: unknown): Promise<T> {
  const response = await fetch(path, body === undefined ? {} : {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
  });
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || "Stream request failed.");
  return result;
}

export default function StreamManager() {
  const [streams, setStreams] = useState<Stream[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [title, setTitle] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ id: string; title: string } | null>(null);
  const working = useRef(false);
  const generation = useRef(0);
  const selected = streams.find((stream) => stream.id === selectedID);

  useEffect(() => {
    let active = true;
    let refreshing = false;
    const refresh = async () => {
      if (working.current || refreshing) return;
      refreshing = true;
      const version = generation.current;
      try {
        const result = await request<Stream[]>("/api/account/streams");
        if (active && version === generation.current) setStreams(result);
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
    <section>
      <h2>Your streams</h2>
      {error && <p role="alert">{error}</p>}
      <form onSubmit={(event) => { event.preventDefault(); void action("create"); }}>
        <label>Stream title <input value={title} maxLength={200} required disabled={busy}
          onChange={(event) => { pending.current = null; setTitle(event.target.value); }} /></label>
        <button disabled={busy || !title.trim()}>Create stream</button>
      </form>
      <label>Stream <select value={selectedID} disabled={busy} onChange={(event) => setSelectedID(event.target.value)}>
        <option value="">Select a stream</option>
        {streams.map((stream) => <option key={stream.id} value={stream.id}>{stream.title} ({stream.state})</option>)}
      </select></label>
      <StreamInvitations streamID={selected?.creatorId === selected?.publisherId && selected?.state !== "ended" ? selected?.id : undefined}
        onChanged={() => { generation.current++; }} />
      {selected && (
        <section>
          <h3>{selected.title}</h3>
          <p role="status">{selected.state}</p>
          <button disabled={busy || selected.state !== "ready" || selected.creatorId !== selected.publisherId} onClick={() => void action("start")}>Start stream</button>
          <button disabled={busy || selected.state === "ended" || selected.creatorId !== selected.publisherId} onClick={() => void action("end")}>End stream for everyone</button>
          {selected.creatorId !== selected.publisherId && selected.state !== "ended" && (
            <button disabled={busy} onClick={() => void action("leave")}>Leave stream</button>
          )}
          {selected.state === "live" && <RelayConnection key={`publish-${selected.id}`}
            streamID={selected.id} />}
          {selected.state === "live" && <SharedStreamViewer key={`view-${selected.id}`} streamID={selected.id} ownAccountID={selected.publisherId} />}
        </section>
      )}
    </section>
  );
}
