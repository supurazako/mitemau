import { useEffect, useState } from "react";
import { AppLink } from "./AppLink";

type Profile = { displayName: string; displayId: string };

export default function AccountProfile() {
  const [profile, setProfile] = useState<Profile>();
  const [savedId, setSavedId] = useState("");
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    async function load() {
      try {
        const response = await fetch("/api/account/profile", { signal: controller.signal });
        if (!response.ok) throw new Error("Could not load profile.");
        const data: Profile = await response.json();
        if (!controller.signal.aborted) {
          setProfile(data);
          setSavedId(data.displayId);
        }
      } catch (cause) {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause));
      }
    }
    void load();
    return () => controller.abort();
  }, []);

  async function save() {
    if (!profile || busy) return;
    setBusy(true);
    setStatus("");
    setError("");
    try {
      const response = await fetch("/api/account/profile", {
        method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(profile),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "Could not save profile.");
      setProfile(data);
      setSavedId(data.displayId);
      setStatus("Profile saved");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setBusy(false);
    }
  }

  return <section aria-label="Public profile">
    <h2>Public profile</h2>
    {profile && <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <label>Display name <input required maxLength={80} value={profile.displayName} disabled={busy}
        onChange={(event) => setProfile({ ...profile, displayName: event.target.value })} /></label>
      <label>Display ID <input required minLength={3} maxLength={24} autoCapitalize="none" spellCheck={false}
        pattern={'[A-Za-z0-9][A-Za-z0-9_\\-]{2,23}'} value={profile.displayId} disabled={busy}
        onChange={(event) => setProfile({ ...profile, displayId: event.target.value })} /></label>
      <small>3–24 letters, numbers, underscores or hyphens. Start with a letter or number.</small>
      <button disabled={busy}>Save profile</button>
    </form>}
    {savedId && <AppLink href={`/users/${savedId}`}>View your public page</AppLink>}
    {status && <p role="status">{status}</p>}
    {error && <p role="alert">{error}</p>}
  </section>;
}
