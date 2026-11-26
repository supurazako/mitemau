import { useEffect, useRef, useState } from "react";

export default function ForceStreamEnd({ streamId, reportId }: { streamId: string; reportId?: string }) {
  const [reason, setReason] = useState("");
  const [publicReason, setPublicReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);
  async function end() {
    if (request.current) return;
    const controller = new AbortController(); request.current = controller;
    setBusy(true); setStatus("");
    try {
      const response = await fetch(`/api/staff/streams/${streamId}/end`, { method: "POST", signal: controller.signal, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ reason, publicReason, reportId: reportId || "" }) });
      if (!response.ok) throw new Error((await response.json()).error || "Could not end stream");
      if (!controller.signal.aborted) setStatus("Stream ended for everyone");
    } catch (cause) { if (!controller.signal.aborted) setStatus(cause instanceof Error ? cause.message : String(cause)); }
    finally { request.current = null; if (!controller.signal.aborted) setBusy(false); }
  }
  return <section aria-label="Force stream end">
    <p>End stream {streamId} for all participants. This stream cannot be restarted.</p>
    <form onSubmit={(event) => { event.preventDefault(); void end(); }}>
      <label>Staff termination reason (private) <textarea required disabled={busy} value={reason} onChange={(event) => setReason(event.target.value)} /></label>
      <label>Termination reason visible to participants <textarea required disabled={busy} value={publicReason} onChange={(event) => setPublicReason(event.target.value)} /></label>
      <button disabled={busy || !reason.trim() || !publicReason.trim()}>End stream for everyone</button>
    </form>
    {status && <p role="status">{status}</p>}
  </section>;
}
export function ForceStreamEndLookup({ initialStreamId = "", reportId }: { initialStreamId?: string; reportId?: string }) {
  const [input, setInput] = useState(initialStreamId);
  const [stream, setStream] = useState(initialStreamId);
  return <section aria-label="Staff stream termination">
    <h4>End a stream for everyone</h4>
    <form onSubmit={(event) => { event.preventDefault(); setStream(input.trim().toLowerCase()); }}>
      <label>Stream ID to end <input required pattern="[a-fA-F0-9]{32}" value={input} onChange={(event) => setInput(event.target.value)} /></label>
      <button>Select stream to end</button>
    </form>
    {stream && <ForceStreamEnd key={stream} streamId={stream} reportId={reportId} />}
  </section>;
}
