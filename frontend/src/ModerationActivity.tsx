import { useEffect, useRef, useState } from "react";

type RecordItem = {
  id: string; kind: string; actorId: string; accountId: string;
  streamId: string | null; commentId: string | null; reportId: string | null;
  occurredAt: string; expiresAt: string; body: string | null;
};
type Page = { items: RecordItem[]; nextCursor: string | null };
const emptyPage: Page = { items: [], nextCursor: null };

export default function ModerationActivity() {
  const [accountId, setAccountId] = useState("");
  const [streamId, setStreamId] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [activity, setActivity] = useState<Page>(emptyPage);
  const [evidence, setEvidence] = useState<Page>(emptyPage);
  const [searched, setSearched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  const filter = useRef("");

  useEffect(() => () => controller.current?.abort(), []);

  async function load(older?: "activity" | "evidence") {
    if (controller.current) return;
    const request = new AbortController();
    controller.current = request;
    setBusy(true);
    setError("");
    try {
      if (!older) {
        filter.current = new URLSearchParams({
          accountId: accountId.trim(), streamId: streamId.trim(),
          from: new Date(from).toISOString(), to: new Date(to).toISOString(),
        }).toString();
        setActivity(emptyPage); setEvidence(emptyPage); setSearched(false);
      }
      async function fetchPage(kind: "activity" | "evidence", cursor?: string | null) {
        const query = new URLSearchParams(filter.current);
        if (cursor) query.set("before", cursor);
        const response = await fetch(`/api/staff/${kind}?${query}`, { signal: request.signal });
        if (!response.ok) {
          if ((response.status === 401 || response.status === 403) && !request.signal.aborted) {
            setActivity(emptyPage); setEvidence(emptyPage); setSearched(false);
          }
          throw new Error((await response.json()).error || "Could not load investigation records.");
        }
        return response.json() as Promise<Page>;
      }
      if (older) {
        const cursor = older === "activity" ? activity.nextCursor : evidence.nextCursor;
        if (!cursor) return;
        const page = await fetchPage(older, cursor);
        if (!request.signal.aborted) {
          const update = (current: Page): Page => ({ items: [...current.items, ...page.items], nextCursor: page.nextCursor });
          if (older === "activity") setActivity(update); else setEvidence(update);
        }
      } else {
        const [events, snapshots] = await Promise.all([fetchPage("activity"), fetchPage("evidence")]);
        if (!request.signal.aborted) { setActivity(events); setEvidence(snapshots); setSearched(true); }
      }
    } catch (cause) {
      if (!request.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      if (controller.current === request) controller.current = null;
      if (!request.signal.aborted) setBusy(false);
    }
  }

  function records(title: string, page: Page, kind: "activity" | "evidence") {
    return <section aria-label={title}>
      <h4>{title}</h4>
      {page.items.length === 0 && <p>No retained records match this search.</p>}
      <ol>{page.items.map((item) => <li key={item.id}>
        <p>{item.kind} — {new Date(item.occurredAt).toLocaleString()}</p>
        <p>Actor: {item.actorId}</p>
        <p>Subject: {item.accountId}</p>
        {item.streamId && <p>Stream: {item.streamId}</p>}
        {item.commentId && <p>Comment: {item.commentId}</p>}
        {item.reportId && <p>Report: {item.reportId}</p>}
        {item.body !== null && <blockquote style={{ whiteSpace: "pre-wrap" }}>{item.body}</blockquote>}
        <p>Record expires: {new Date(item.expiresAt).toLocaleString()}</p>
      </li>)}</ol>
      {page.nextCursor && <button disabled={busy} onClick={() => void load(kind)}>Load older {kind}</button>}
    </section>;
  }

  return <section aria-label="Moderation investigation">
    <h3>Moderation investigation</h3>
    <p>Staff only. Times below use your local timezone. Expired records and comment bodies are unavailable.</p>
    <form onSubmit={(event) => { event.preventDefault(); void load(); }}>
      <label>Investigate account ID <input value={accountId} onChange={(event) => setAccountId(event.target.value)} required pattern="[0-9a-fA-F]{64}" disabled={busy} /></label>
      <label>From <input type="datetime-local" required value={from} onChange={(event) => setFrom(event.target.value)} disabled={busy} /></label>
      <label>To <input type="datetime-local" required value={to} onChange={(event) => setTo(event.target.value)} disabled={busy} /></label>
      <label>Filter stream ID (optional) <input value={streamId} pattern="[0-9a-fA-F]{32}" onChange={(event) => setStreamId(event.target.value)} disabled={busy} /></label>
      <button disabled={busy}>Search records</button>
    </form>
    {busy && <p role="status">Loading investigation records…</p>}
    {error && <p role="alert">{error}</p>}
    {searched && <>{records("Activity", activity, "activity")}{records("Report evidence", evidence, "evidence")}</>}
  </section>;
}
