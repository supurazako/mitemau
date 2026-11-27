import { useEffect, useId, useRef, useState } from "react";
import { AppLink } from "./AppLink";
import { useViewer } from "./lib/viewer";

import type { LiveComment } from "./lib/comments";

export default function ReportUser({ targetId, streamId, streamTitle, relatedComment, openRequest }: {
  targetId: string;
  streamId?: string;
  streamTitle?: string;
  relatedComment?: LiveComment;
  openRequest?: number;
}) {
  const viewer = useViewer();
  const dialog = useRef<HTMLDialogElement>(null);
  const headingId = useId();
  useEffect(() => {
    if (openRequest && dialog.current && !dialog.current.open) dialog.current.showModal();
  }, [openRequest]);
  const [reason, setReason] = useState("");
  const [author, setAuthor] = useState<{ displayName?: string; displayId?: string }>();
  const authorId = relatedComment?.accountId;
  useEffect(() => {
    if (!authorId || !viewer.signedIn) return;
    const controller = new AbortController();
    async function loadAuthor() {
      try {
        const response = await fetch(`/api/public/users/${authorId}`, {
          credentials: "omit", signal: controller.signal,
        });
        if (!response.ok) return;
        const data = await response.json();
        if (!controller.signal.aborted) setAuthor(data);
      } catch {
        // The comment preview remains usable if the profile lookup fails.
      }
    }
    void loadAuthor();
    return () => controller.abort();
  }, [authorId, viewer.signedIn]);
  const [incident, setIncident] = useState("");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit() {
    if (busy) return;
    setBusy(true); setStatus("");
    try {
      const response = await fetch("/api/reports", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ targetId, reason, incidentAt: new Date(incident).toISOString(), streamId: streamId || "", commentId: relatedComment?.id || "" }) });
      if (!response.ok) throw new Error((await response.json()).error || "Could not submit report");
      setStatus("Report submitted"); setReason(""); setIncident(""); dialog.current?.close();
    } catch (cause) { setStatus(cause instanceof Error ? cause.message : String(cause)); }
    finally { setBusy(false); }
  }
  if (!viewer.ready || !viewer.signedIn || viewer.accountId === targetId) return null;
  return <section className="report-user" aria-label="Report user">
    {!relatedComment && <button type="button" aria-haspopup="dialog" onClick={() => { setStatus(""); dialog.current?.showModal(); }}>Report user</button>}
    <dialog ref={dialog} className="report-dialog" aria-labelledby={headingId}
      onCancel={(event) => { if (busy) event.preventDefault(); }}>
      <h2 id={headingId}>Report user</h2>
      <form onSubmit={(event) => { event.preventDefault(); void submit(); }}>
        <p>Reports are reviewed by staff.</p>
        {relatedComment && <figure className="report-comment-preview">
          <figcaption>
            <AppLink href={author?.displayId ? `/users/${author.displayId}` : `/${relatedComment.accountId}`}>
              {author?.displayName || (author?.displayId ? `@${author.displayId}` : "Comment author")}
            </AppLink>
            {author?.displayName && author.displayId && <> @{author.displayId}</>}
            {" · "}<time dateTime={relatedComment.postedAt}>
            {new Date(relatedComment.postedAt).toLocaleString()}
          </time></figcaption>
          <blockquote>{relatedComment.body}</blockquote>
        </figure>}
        {streamTitle && <p>Related stream: {streamTitle}</p>}
        <label>What happened? <textarea required value={reason} onChange={(e) => setReason(e.target.value)} /></label>
        <label>When did it happen? <input required type="datetime-local" value={incident} onChange={(e) => setIncident(e.target.value)} /></label>
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
