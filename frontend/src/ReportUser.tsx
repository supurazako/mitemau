import { useEffect, useId, useRef, useState } from "react";

export default function ReportUser({ targetId, streamId, relatedCommentId, initiallyOpen = false }: { targetId: string; streamId?: string; relatedCommentId?: string; initiallyOpen?: boolean }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const headingId = useId();
  useEffect(() => {
    if (initiallyOpen && dialog.current && !dialog.current.open) dialog.current.showModal();
  }, [initiallyOpen]);
  const [reason, setReason] = useState("");
  const [incident, setIncident] = useState("");
  const [commentId, setCommentId] = useState(relatedCommentId || "");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit() {
    if (busy) return;
    setBusy(true); setStatus("");
    try {
      const response = await fetch("/api/reports", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ targetId, reason, incidentAt: new Date(incident).toISOString(), streamId: streamId || "", commentId: commentId.trim() }) });
      if (!response.ok) throw new Error((await response.json()).error || "Could not submit report");
      setStatus("Report submitted"); setReason(""); setCommentId(relatedCommentId || ""); setIncident(""); dialog.current?.close();
    } catch (cause) { setStatus(cause instanceof Error ? cause.message : String(cause)); }
    finally { setBusy(false); }
  }
  return <section className="report-user" aria-label="Report user">
    <button type="button" aria-haspopup="dialog" onClick={() => { setStatus(""); dialog.current?.showModal(); }}>Report user</button>
    <dialog ref={dialog} className="report-dialog" aria-labelledby={headingId}
      onCancel={(event) => { if (busy) event.preventDefault(); }}>
      <h2 id={headingId}>Report user</h2>
      <form onSubmit={(event) => { event.preventDefault(); void submit(); }}>
        <p>Sign in to report this user. Reports are reviewed by staff.</p>
        <label>What happened? <textarea required value={reason} onChange={(e) => setReason(e.target.value)} /></label>
        <label>When did it happen? <input required type="datetime-local" value={incident} onChange={(e) => setIncident(e.target.value)} /></label>
        <label>Related comment ID (optional) <input pattern="[a-fA-F0-9]{32}" value={commentId} onChange={(e) => setCommentId(e.target.value)} /></label>
        {status && status !== "Report submitted" && <p role="alert">{status}</p>}
        <div className="report-dialog-actions">
          <button type="button" disabled={busy} onClick={() => dialog.current?.close()}>Cancel</button>
          <button disabled={busy}>Submit report</button>
        </div>
      </form>
    </dialog>
    {status === "Report submitted" && <p role="status">{status}</p>}
  </section>;
}
