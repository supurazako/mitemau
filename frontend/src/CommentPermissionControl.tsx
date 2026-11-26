import { useEffect, useRef, useState } from "react";

type Permission = { postingAllowed: boolean; reason: string | null };
export default function CommentPermissionControl({ accountId, reportId }: { accountId: string; reportId?: string }) {
  const [permission, setPermission] = useState<Permission>();
  const [reason, setReason] = useState("");
  const [publicReason, setPublicReason] = useState("");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const request = useRef<AbortController | null>(null);
  const path = `/api/staff/accounts/${accountId}/comment-permission`;

  useEffect(() => {
    const controller = new AbortController();
    void fetch(path, { signal: controller.signal, cache: "no-store" }).then(async (response) => {
      if (!response.ok) throw new Error("Could not load posting permission");
      const data = await response.json();
      if (!controller.signal.aborted) setPermission(data);
    }).catch((cause) => { if (!controller.signal.aborted) setStatus(String(cause)); });
    return () => { controller.abort(); request.current?.abort(); };
  }, [path]);

  async function change() {
    if (running.current || !permission) return;
    running.current = true; setBusy(true); setStatus("");
    const controller = new AbortController(); request.current = controller;
    const postingAllowed = !permission.postingAllowed;
    try {
      const response = await fetch(path, { method: "POST", signal: controller.signal, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ postingAllowed, reason, publicReason: postingAllowed ? "" : publicReason, reportId: reportId || "" }) });
      if (!response.ok) throw new Error((await response.json()).error || "Could not change permission");
      // Reload authoritative state: another staff member may have acted meanwhile.
      const current = await fetch(path, { signal: controller.signal, cache: "no-store" });
      if (!current.ok) throw new Error("Change accepted; could not refresh permission");
      const data = await current.json();
      if (!controller.signal.aborted) { setPermission(data); setReason(""); setPublicReason(""); setStatus("Posting permission updated"); }
    } catch (cause) { if (!controller.signal.aborted) setStatus(cause instanceof Error ? cause.message : String(cause)); }
    finally { running.current = false; if (!controller.signal.aborted) setBusy(false); }
  }
  return <section aria-label="Comment posting permission">
    <h4>Comment posting permission</h4>
    {permission && <>
      <p>{permission.postingAllowed ? "Posting allowed" : "Posting suspended until staff restore permission"}</p>
      {!permission.postingAllowed && <p>Reason visible to account: {permission.reason}</p>}
      <form onSubmit={(event) => { event.preventDefault(); void change(); }}>
        <label>Staff reason (private) <textarea required disabled={busy} value={reason} onChange={(event) => setReason(event.target.value)} /></label>
        {permission.postingAllowed && <label>Reason visible to account <textarea required disabled={busy} value={publicReason} onChange={(event) => setPublicReason(event.target.value)} /></label>}
        <button disabled={busy || !reason.trim() || (permission.postingAllowed && !publicReason.trim())}>{permission.postingAllowed ? "Suspend posting" : "Restore posting"}</button>
      </form>
    </>}
    {status && <p role="status">{status}</p>}
  </section>;
}

export function CommentPermissionLookup() {
  const [input, setInput] = useState("");
  const [account, setAccount] = useState("");
  return <section aria-label="Posting permission lookup">
    <h3>Manage comment posting</h3>
    <form onSubmit={(event) => { event.preventDefault(); setAccount(input.trim().toLowerCase()); }}>
      <label>Account ID <input required pattern="[a-fA-F0-9]{64}" value={input} onChange={(event) => setInput(event.target.value)} /></label>
      <button>Load posting permission</button>
    </form>
    {account && <CommentPermissionControl key={account} accountId={account} />}
  </section>;
}
