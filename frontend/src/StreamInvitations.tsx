import { useEffect, useRef, useState } from "react";

type Invitation = { id: string; title: string };
export default function StreamInvitations({ streamID, onChanged }: {
  streamID?: string;
  onChanged: () => void;
}) {
  const [accountID, setAccountID] = useState("");
  const [target, setTarget] = useState("");
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const generation = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    const version = ++generation.current;
    async function load() {
      const response = await fetch("/api/account/broadcaster", { signal: controller.signal });
      if (!response.ok) throw new Error("Could not load account ID.");
      const account = await response.json();
      const pending = await fetch("/api/account/invitations", { signal: controller.signal });
      if (!pending.ok) throw new Error("Could not load invitations.");
      const list: Invitation[] = await pending.json();
      if (!controller.signal.aborted && version === generation.current) {
        setAccountID(account.accountId);
        setInvitations(list);
      }
    }
    void load().catch((cause) => { if (!controller.signal.aborted && version === generation.current) setError(String(cause)); });
    return () => controller.abort();
  }, [streamID]);

  async function act(path: string, body: object) {
    if (running.current) return;
    running.current = true;
    const version = ++generation.current;
    setBusy(true);
    setError("");
    try {
      const response = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      if (!response.ok) throw new Error((await response.json()).error || "Could not update invitation.");
      const pending = await fetch("/api/account/invitations");
      if (!pending.ok) throw new Error("Could not load invitations.");
      const list: Invitation[] = await pending.json();
      if (version === generation.current) setInvitations(list);
      onChanged();
    } catch (cause) { if (version === generation.current) setError(String(cause)); }
    finally { running.current = false; setBusy(false); }
  }

  return (
    <section>
      <h3>Collaboration invitations</h3>
      {accountID && <label>Your account ID <input readOnly value={accountID} onFocus={(event) => event.target.select()} /></label>}
      {error && <p role="alert">{error}</p>}
      {streamID && <form onSubmit={(event) => {
        event.preventDefault();
        void act(`/api/streams/${streamID}/invite`, { accountId: target.trim() });
      }}>
        <label>Invite account ID <input value={target} required disabled={busy}
          onChange={(event) => setTarget(event.target.value)} /></label>
        <button disabled={busy}>Invite</button>
      </form>}
      <ul>{invitations.map((invitation) => <li key={invitation.id}>
        {invitation.title} <button disabled={busy} onClick={() => void act(`/api/streams/${invitation.id}/join`, {})}>Accept invitation</button>
      </li>)}</ul>
      <p>Reload this page to check for new invitations.</p>
    </section>
  );
}
