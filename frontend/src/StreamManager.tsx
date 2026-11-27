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
  const [draftTitle, setDraftTitle] = useState<string | null>(null);
  // The account stream list is ordered newest first.
  const previousTitle = streams.find((stream) =>
    stream.state === "ended" && stream.creatorId === stream.publisherId
  )?.title;
  const title = draftTitle ?? previousTitle ?? "";
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ id: string; title: string } | null>(null);
  const working = useRef(false);
  const generation = useRef(0);
  const current = streams.find((stream) => stream.state === "live")
    ?? streams.find((stream) => stream.state === "ready");
  const endNotice = !current ? streams.find((stream) => stream.state === "ended")?.endReason : undefined;
  const isCreator = current && current.creatorId === current.publisherId;

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
        const created = await request<Stream>("/api/streams", pending.current);
        setStreams((items) => [created, ...items.filter((item) => item.id !== created.id)]);
        pending.current = null;
        setDraftTitle(null);
        updated = await request<Stream>(`/api/streams/${created.id}/start`, {});
      } else {
        if (!current) return;
        if (operation === "leave") {
          await request(`/api/streams/${current.id}/leave`, {});
          setStreams((items) => items.filter((item) => item.id !== current.id));
          return;
        }
        updated = await request<Stream>(`/api/streams/${current.id}/${operation}`, {});
      }
      setStreams((items) => [updated, ...items.filter((item) => item.id !== updated.id)]);
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
      {endNotice && <p role="status">Stream ended by staff: {endNotice}</p>}
      {error && <p role="alert">{error}</p>}
      {access && !current && <div className="studio-setup">
        <form className="studio-panel" onSubmit={(event) => { event.preventDefault(); void action("create"); }}>
          <h3>Your channel is offline</h3>
          <label>Stream title <input value={title} maxLength={200} required disabled={busy}
            onChange={(event) => { pending.current = null; setDraftTitle(event.target.value); }} /></label>
          <button className="primary-action" disabled={busy || !canPublish || !title.trim()}>Go live</button>
        </form>
      </div>}
      {current && (
        <section className="studio-current">
          <header className="studio-panel">
            <p className={current.state === "live" ? "live-badge" : "studio-state"} role="status">{stateLabel[current.state]}</p>
            <h3>{current.title}</h3>
            <p>{isCreator ? "You created this stream." : "You joined as a participant."}</p>
            {current.endReason && <p role="status">Stream ended by staff: {current.endReason}</p>}
            {isCreator && current.state === "ready" &&
              <button className="primary-action" disabled={busy || !canPublish} onClick={() => void action("start")}>Start stream</button>}
            {current.state !== "ended" && <div className="studio-exit">
              {isCreator
                ? <button className="danger-action" disabled={busy} onClick={() => {
                  if (window.confirm("End this stream for everyone? This stream cannot be restarted.")) void action("end");
                }}>End stream for everyone</button>
                : <button disabled={busy} onClick={() => void action("leave")}>Leave stream</button>}
            </div>}
          </header>
          {current.state === "live" && <div className="watch-layout">
            <div className="watch-media">
              {canPublish && <RelayConnection key={`publish-${current.id}`} streamID={current.id} />}
              <SharedStreamViewer key={`view-${current.id}`} streamID={current.id} ownAccountID={current.publisherId} />
            </div>
            <LiveComments key={`comments-${current.id}`} streamID={current.id} />
          </div>}
        </section>
      )}
    </section>
    <div hidden={!invitationsPage}>
      <StreamInvitations key={invitee ?? ""} invitee={invitee}
        hostStream={canPublish && isCreator && current?.state === "live" ? current : undefined}
        activeStreamId={current?.state === "live" ? current.id : undefined}
        onChanged={() => { generation.current++; }} />
    </div>
    </>
  );
}
