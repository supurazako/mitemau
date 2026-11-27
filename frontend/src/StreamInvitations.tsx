import { AppLink } from "./AppLink";
import { useEffect, useRef, useState } from "react";

type Invitation = { id: string; title: string };
export default function StreamInvitations({ invitee, hostStream, activeStreamId, canPublish, onChanged }: {
  invitee?: string;
  hostStream?: { id: string; title: string };
  activeStreamId?: string;
  canPublish: boolean;
  onChanged: () => void;
}) {
  const [displayID, setDisplayID] = useState("");
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  const generation = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    const version = ++generation.current;
    async function load() {
      const pending = await fetch("/api/account/invitations", { signal: controller.signal });
      if (!pending.ok) throw new Error("Could not load invitations.");
      const list: Invitation[] = await pending.json();
      if (!controller.signal.aborted && version === generation.current) {
        setInvitations(list);
      }
    }
    void load().catch((cause) => { if (!controller.signal.aborted && version === generation.current) setError(String(cause)); });
    return () => controller.abort();
  }, []);

  async function act(path: string, body: object) {
    if (running.current) return;
    running.current = true;
    const version = ++generation.current;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const response = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
      if (!response.ok) throw new Error((await response.json()).error || "Could not update invitation.");
      const pending = await fetch("/api/account/invitations");
      if (!pending.ok) throw new Error("Could not load invitations.");
      const list: Invitation[] = await pending.json();
      if (version === generation.current) setInvitations(list);
      setNotice(path.endsWith("/invite") ? "Invitation sent" : "Invitation accepted");
      onChanged();
    } catch (cause) { if (version === generation.current) setError(String(cause)); }
    finally { running.current = false; setBusy(false); }
  }

  return (
    <section className="studio-panel">
      <h3>Collaboration invitations</h3>
      {notice && <p role="status">{notice}</p>}
      {error && <p role="alert">{error}</p>}
      {!canPublish && <p role="status">
        Broadcasting permission is unavailable for this account, so invitations cannot be accepted.
        Check your <AppLink href="/broadcasting">studio</AppLink> for details.
      </p>}
      {canPublish && <>
        {invitee
          ? <InviteToStream invitee={invitee} hostStream={hostStream}
            busy={busy} onInvite={(stream, accountId) => void act(`/api/streams/${stream}/invite`, { accountId })} />
          : hostStream
            ? <form onSubmit={(event) => {
              event.preventDefault();
              const id = displayID.trim().replace(/^@/, "").toLowerCase();
              if (!/^[a-z0-9][a-z0-9_-]{2,23}$/.test(id)) {
                setError("Enter a valid display ID.");
                return;
              }
              window.history.pushState(null, "", `/broadcasting/invitations/@${id}`);
              window.dispatchEvent(new PopStateEvent("popstate"));
            }}>
              <label>Display ID <input required value={displayID} autoCapitalize="none" spellCheck={false}
                onChange={(event) => setDisplayID(event.target.value)} /></label>
              <button>Find user</button>
            </form>
            : <p>Go live from your <AppLink href="/broadcasting">studio</AppLink> to invite someone.</p>}
        {invitations.length > 0 && <ul>{invitations.map((invitation) => <li key={invitation.id}>
          {invitation.title} <button disabled={busy || (!!activeStreamId && activeStreamId !== invitation.id)} onClick={() => void act(`/api/streams/${invitation.id}/join`, {})}>Accept invitation</button>
        </li>)}</ul>}
        {invitations.some(invitation => activeStreamId && invitation.id !== activeStreamId) &&
          <p>End or leave your current stream before joining another.</p>}
        {invitations.length === 0 && <p>No pending invitations</p>}
      </>}
    </section>
  );
}


function InviteToStream({ invitee, hostStream, busy, onInvite }: {
  invitee: string; hostStream?: { id: string; title: string };
  busy: boolean; onInvite: (stream: string, accountId: string) => void;
}) {
  const [profile, setProfile] = useState<{ accountId: string; displayId?: string; displayName?: string; eligible: boolean }>();
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    async function load() {
      try {
        const response = await fetch(invitee.startsWith("@") ? `/api/public/profiles/${invitee.slice(1)}` : `/api/public/users/${invitee}`, { signal: controller.signal });
        if (!response.ok) throw new Error(response.status === 404 ? "User not found." : "Could not look up user.");
        const data = await response.json();
        const eligibility = await fetch(`/api/account/invitations/eligibility/${data.accountId}`, { signal: controller.signal });
        if (!eligibility.ok) throw new Error("Could not check whether this user can be invited.");
        const { eligible } = await eligibility.json();
        if (!controller.signal.aborted) setProfile({ ...data, eligible });
      } catch (cause) {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause));
      }
    }
    void load();
    return () => controller.abort();
  }, [invitee]);

  return <>
    <AppLink href="/broadcasting/invitations">Find another user</AppLink>
    {error && <p role="alert">{error}</p>}
    {profile && <p>Invite {profile.displayName || profile.displayId || "this user"}{profile.displayId && ` (@${profile.displayId})`}</p>}
    {profile && (!profile.eligible ? <p>This user cannot receive a broadcast invitation.</p> : hostStream
      ? <button type="button" disabled={busy} onClick={() => onInvite(hostStream.id, profile.accountId)}>Invite to {hostStream.title}</button>
      : <p>Go live from your <AppLink href="/broadcasting">studio</AppLink> to invite this user.</p>)}

  </>;
}
