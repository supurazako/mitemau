import ReportUser from "./ReportUser";
import { useEffect, useRef, useState } from "react";
import { FullTrackName } from "moqtail";
import { AppLink } from "./AppLink";
import { connectRelay, TEST_RELAY_URL } from "./lib/moq";
import { newerComments, parseCommentSnapshot, untilAborted } from "./lib/comments";
import type { CommentSnapshot } from "./lib/comments";

async function pause(signal: AbortSignal) {
  let timer: number | undefined;
  try { await untilAborted(new Promise<void>((resolve) => { timer = window.setTimeout(resolve, 1000); }), signal); }
  finally { window.clearTimeout(timer); }
}

export default function LiveComments({ streamID }: { streamID: string }) {
  const [snapshot, setSnapshot] = useState<CommentSnapshot>();
  const [status, setStatus] = useState("Connecting comments");
  const [ended, setEnded] = useState(false);
  const [body, setBody] = useState("");
  const [posting, setPosting] = useState(false);
  const [postStatus, setPostStatus] = useState("");
  const [canDelete, setCanDelete] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [deleteStatus, setDeleteStatus] = useState("");
  const deletion = useRef<AbortController | null>(null);
  const pending = useRef<{ requestId: string; body: string } | null>(null);
  const running = useRef(false);
  const view = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const postRequest = useRef<AbortController | null>(null);
  const characters = Array.from(body.trim()).length;

  useEffect(() => {
    const lifetime = new AbortController();
    let finished = false;
    let acceptedVersion = -1n;
    let bootstrapped = false;
    function accept(data: unknown) {
      if (lifetime.signal.aborted || finished) return;
      const next = parseCommentSnapshot(data, streamID);
      if (BigInt(next.version) > acceptedVersion) acceptedVersion = BigInt(next.version);
      setSnapshot((current) => newerComments(current, next));
    }
    function finish() {
      finished = true;
      if (!lifetime.signal.aborted) { setEnded(true); setSnapshot(undefined); setStatus("Comments ended"); }
    }
    async function load(signal: AbortSignal, channel = false) {
      const response = await fetch(`/api/public/streams/${streamID}/comments${channel ? "/channel" : ""}`, { signal, credentials: "omit" });
      if (response.status === 404) { finish(); return null; }
      if (!response.ok) throw new Error("Comment information unavailable");
      return response.json() as Promise<CommentSnapshot>;
    }
    async function run() {
      while (!lifetime.signal.aborted && !finished) {
        const connection = new AbortController();
        const abort = () => connection.abort();
        lifetime.signal.addEventListener("abort", abort, { once: true });
        let close: (() => void) | undefined;
        let timer: number | undefined;
        try {
          const initial = await load(connection.signal, bootstrapped);
          if (!initial || lifetime.signal.aborted) break;
          if (!bootstrapped) { accept(initial); bootstrapped = true; }
          if (!initial.namespace) { setStatus("Waiting for comment publisher"); continue; }
          const namespace = initial.namespace;
          const relay = connectRelay(TEST_RELAY_URL, abort);
          let reader: ReadableStreamDefaultReader<unknown> | undefined;
          close = () => { void reader?.cancel().catch(() => {}); void relay.disconnect().catch(console.error); };
          connection.signal.addEventListener("abort", close, { once: true });
          timer = window.setTimeout(abort, 10000);
          await untilAborted(relay.ready, connection.signal);
          const subscription = await untilAborted(relay.subscribeMedia(FullTrackName.tryNew(namespace, "comments")), connection.signal);
          window.clearTimeout(timer);
          const latest = await load(connection.signal);
          if (!latest) break;
          if (latest.namespace !== namespace) throw new Error("Comment publisher changed");
          accept(latest);
          if (lifetime.signal.aborted) break;
          setStatus("Comments connected");
          let polling = false;
          let missingUpdates = 0;
          timer = window.setInterval(() => {
            if (polling || connection.signal.aborted) return;
            polling = true;
            void load(connection.signal, true).then((channel) => {
              if (!channel || channel.namespace !== namespace) { connection.abort(); return; }
              // If a reliable subgroup was abandoned, reconnect and repair the
              // latest window instead of waiting forever for another comment.
              if (BigInt(channel.version) > acceptedVersion) missingUpdates++; else missingUpdates = 0;
              if (missingUpdates >= 2) connection.abort();
            }).catch(() => connection.abort()).finally(() => { polling = false; });
          }, 2000);
          const objects = subscription.stream.getReader(); reader = objects;
          try {
            while (!connection.signal.aborted) {
              const { value, done } = await untilAborted(objects.read(), connection.signal);
              if (done) break;
              if (value.payload) {
                const incoming = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(value.payload));
                if (incoming.namespace !== namespace) throw new Error("Invalid comment publisher");
                accept(incoming);
              }
            }
          } finally { void objects.cancel().catch(() => {}); objects.releaseLock(); }
        } catch {
          if (!lifetime.signal.aborted && !finished) setStatus("Reconnecting comments");
        } finally {
          window.clearTimeout(timer); window.clearInterval(timer);
          connection.abort(); close?.();
          lifetime.signal.removeEventListener("abort", abort);
          if (!lifetime.signal.aborted && !finished) await pause(lifetime.signal).catch(() => {});
        }
      }
    }
    void run();
    return () => { lifetime.abort(); postRequest.current?.abort(); };
  }, [streamID]);

  useEffect(() => {
    const controller = new AbortController();
    let loading = false;
    async function refresh() {
      if (loading) return;
      loading = true;
      try {
        const response = await fetch(`/api/streams/${streamID}/comments/permissions`, { signal: controller.signal, cache: "no-store" });
        const permitted = response.ok && (await response.json()).canDelete === true;
        if (!controller.signal.aborted) setCanDelete(permitted);
      } catch { if (!controller.signal.aborted) setCanDelete(false); }
      finally { loading = false; }
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2000);
    return () => { controller.abort(); deletion.current?.abort(); window.clearInterval(timer); };
  }, [streamID]);

  async function removeComment(id: string) {
    if (deletion.current || ended) return;
    const controller = new AbortController(); deletion.current = controller;
    setDeleting(id); setDeleteStatus("");
    try {
      const response = await fetch(`/api/streams/${streamID}/comments/${id}`, { method: "DELETE", signal: controller.signal, headers: { "Content-Type": "application/json" } });
      if (!response.ok) {
        if (response.status === 401 || response.status === 403 || response.status === 409) setCanDelete(false);
        throw new Error((await response.json()).error || "Could not delete comment");
      }
      if (!controller.signal.aborted) setDeleteStatus("Comment deleted");
      // The versioned MoQT snapshot updates every viewer, including this page.
    } catch (cause) {
      if (!controller.signal.aborted) setDeleteStatus(cause instanceof Error ? cause.message : String(cause));
    } finally {
      deletion.current = null;
      if (!controller.signal.aborted) setDeleting(null);
    }
  }

  useEffect(() => { if (pinned.current && view.current) view.current.scrollTop = view.current.scrollHeight; }, [snapshot]);

  async function post() {
    if (running.current || ended || characters === 0 || characters > 500) return;
    running.current = true; setPosting(true); setPostStatus("");
    const request = new AbortController(); postRequest.current = request;
    const text = body.trim();
    if (pending.current?.body !== text) pending.current = { requestId: crypto.randomUUID().replaceAll("-", ""), body: text };
    try {
      const response = await fetch(`/api/streams/${streamID}/comments`, { method: "POST", signal: request.signal, headers: { "Content-Type": "application/json" }, body: JSON.stringify(pending.current) });
      if (!response.ok) throw new Error((await response.json()).error || "Could not post comment");
      if (!request.signal.aborted) { setBody(""); pending.current = null; pinned.current = true; setPostStatus("Comment sent"); }
    } catch (cause) {
      if (!request.signal.aborted) setPostStatus(cause instanceof Error ? cause.message : String(cause));
    } finally { running.current = false; if (!request.signal.aborted) setPosting(false); }
  }

  return <section className="live-comments" aria-label="Live comments">
    <h3>Live comments</h3>
    <p role="status">{status}</p>
    <div className="comment-feed" ref={view} onScroll={() => { const element = view.current; if (element) pinned.current = element.scrollHeight - element.scrollTop - element.clientHeight < 24; }}>
      <ol>{snapshot?.comments.map((comment) => <li key={comment.id} data-comment-id={comment.id}>
        <AppLink href={`/${comment.accountId}`}>{comment.accountId.slice(0, 10)}</AppLink>{" "}<time dateTime={comment.postedAt}>{new Date(comment.postedAt).toLocaleTimeString()}</time>
        <p>{comment.body}</p>
        <ReportUser targetId={comment.accountId} streamId={streamID} relatedCommentId={comment.id} />
        {canDelete && !ended && <button type="button" disabled={deleting !== null} onClick={() => void removeComment(comment.id)} aria-label={`Delete comment by ${comment.accountId.slice(0, 10)} at ${new Date(comment.postedAt).toLocaleTimeString()}`}>{deleting === comment.id ? "Deleting…" : "Delete"}</button>}
      </li>)}</ol>
    </div>
    {!ended && <form onSubmit={(event) => { event.preventDefault(); void post(); }}>
      <label>Comment <textarea value={body} disabled={posting} onChange={(event) => { setBody(event.target.value); pending.current = null; }} required /></label>
      <p>{characters} / 500 · Latest 50 comments · One post per two seconds across all streams</p>
      <button disabled={posting || characters === 0 || characters > 500}>Post comment</button>
      <p>Posting requires sign-in. <AppLink href="/account">Account &amp; sign-in</AppLink></p>
    </form>}
    {deleteStatus && <p role="status">{deleteStatus}</p>}
    {postStatus && <p role="status">{postStatus}</p>}
  </section>;
}
