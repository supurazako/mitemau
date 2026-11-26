import { useEffect, useRef, useState } from "react";

type Permission = { broadcastingAllowed: boolean; reason: string | null };
export default function BroadcastPermissionControl({ accountId, reportId }: { accountId: string; reportId?: string }) {
  const [permission, setPermission] = useState<Permission>();
  const [reason, setReason] = useState("");
  const [publicReason, setPublicReason] = useState("");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const request = useRef<AbortController | null>(null);
  const path = `/api/staff/accounts/${accountId}/broadcast-permission`;

  useEffect(() => {
    const controller = new AbortController();
    void fetch(path, { signal: controller.signal, cache: "no-store" }).then(async (response) => {
      if (!response.ok) throw new Error("Could not load broadcasting permission");
      const data = await response.json();
      if (!controller.signal.aborted) setPermission(data);
    }).catch((cause) => { if (!controller.signal.aborted) setStatus(String(cause)); });
    return () => { controller.abort(); request.current?.abort(); };
  }, [path]);

  async function change() {
    if (running.current || !permission) return;
    running.current = true; setBusy(true); setStatus("");
    const controller = new AbortController(); request.current = controller;
    const broadcastingAllowed = !permission.broadcastingAllowed;
    try {
      const response = await fetch(path, { method: "POST", signal: controller.signal, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ broadcastingAllowed, reason, publicReason: broadcastingAllowed ? "" : publicReason, reportId: reportId || "" }) });
      if (!response.ok) throw new Error((await response.json()).error || "Could not change permission");
      // Reload authoritative state: another staff member may have acted meanwhile.
      const current = await fetch(path, { signal: controller.signal, cache: "no-store" });
      if (!current.ok) throw new Error("Change accepted; could not refresh permission");
      const data = await current.json();
      if (!controller.signal.aborted) { setPermission(data); setReason(""); setPublicReason(""); setStatus("Broadcasting permission updated"); }
    } catch (cause) { if (!controller.signal.aborted) setStatus(cause instanceof Error ? cause.message : String(cause)); }
    finally { running.current = false; if (!controller.signal.aborted) setBusy(false); }
  }
  return <section aria-label="Broadcasting permission">
    <h4>Broadcasting permission</h4>
    {permission && <>
      <p>{permission.broadcastingAllowed ? "Broadcasting allowed (requires broadcaster approval)" : "Broadcasting suspended until staff restore permission"}</p>
      {!permission.broadcastingAllowed && <p>Reason visible to account: {permission.reason}</p>}
      <form onSubmit={(event) => { event.preventDefault(); void change(); }}>
        <label>Staff reason (private) <textarea required disabled={busy} value={reason} onChange={(event) => setReason(event.target.value)} /></label>
        {permission.broadcastingAllowed && <label>Reason visible to account <textarea required disabled={busy} value={publicReason} onChange={(event) => setPublicReason(event.target.value)} /></label>}
        <button disabled={busy || !reason.trim() || (permission.broadcastingAllowed && !publicReason.trim())}>{permission.broadcastingAllowed ? "Suspend broadcasting" : "Restore broadcasting"}</button>
      </form>
    </>}
    {status && <p role="status">{status}</p>}
  </section>;
}

export function BroadcastPermissionLookup() {
  const [input, setInput] = useState("");
  const [account, setAccount] = useState("");
  return <section aria-label="Broadcasting permission lookup">
    <h3>Manage broadcasting</h3>
    <form onSubmit={(event) => { event.preventDefault(); setAccount(input.trim().toLowerCase()); }}>
      <label>Account ID <input required pattern="[a-fA-F0-9]{64}" value={input} onChange={(event) => setInput(event.target.value)} /></label>
      <button>Load broadcasting permission</button>
    </form>
    {account && <BroadcastPermissionControl key={account} accountId={account} />}
  </section>;
}
