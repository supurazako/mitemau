import ReportUser from "./ReportUser";
import LiveComments from "./LiveComments";
import { useEffect, useState } from "react";
import CameraViewer from "./CameraViewer";
import { AppLink } from "./AppLink";

type PublicStream = { id: string; creatorId: string; title: string };
type LiveListing = PublicStream & { watchAccountId: string | null };
type Participant = { accountId: string; namespace: string | null; publishingState: "stopped" | "publishing" | "disconnected" };
type StreamDetail = { stream: PublicStream; participants: Participant[] };
type UserStream = { accountId: string; displayId?: string; displayName?: string; stream: PublicStream | null };

function usePublicResource<T>(path: string) {
  const [data, setData] = useState<T>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [unavailable, setUnavailable] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    let refreshing = false;
    async function refresh() {
      if (refreshing) return;
      refreshing = true;
      try {
        const response = await fetch(path, { signal: controller.signal, credentials: "omit" });
        if (response.status === 404) {
          if (!controller.signal.aborted) {
            setData(undefined);
            setUnavailable(true);
            setError("");
          }
          return;
        }
        if (!response.ok) throw new Error("Could not load streaming information.");
        const result: T = await response.json();
        if (!controller.signal.aborted) {
          setData(result);
          setUnavailable(false);
          setError("");
        }
      } catch (cause) {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause));
      } finally {
        if (!controller.signal.aborted) setLoading(false);
        refreshing = false;
      }
    }
    void refresh();
    const timer = window.setInterval(() => void refresh(), 2000);
    return () => {
      controller.abort();
      window.clearInterval(timer);
    };
  }, [path, attempt]);

  return {
    data, error, loading, unavailable,
    reload() {
      setLoading(true);
      setAttempt((value) => value + 1);
    },
  };
}

function LoadError({ message, reload }: { message: string; reload: () => void }) {
  if (!message) return null;
  return <div role="alert"><p>{message}</p><button onClick={reload}>Try again</button></div>;
}

export function LiveList() {
  const resource = usePublicResource<LiveListing[]>("/api/public/streams");
  return <main className="public-page">
    <header className="page-heading">
      <h1>Live streams</h1>
    </header>
    {resource.loading && !resource.data && <p className="public-notice" role="status">Loading live streams…</p>}
    <LoadError message={resource.error || (resource.unavailable ? "Live streams are unavailable." : "")} reload={resource.reload} />
    {!resource.error && resource.data?.length === 0 &&
      <p className="public-notice" role="status">No streams are live right now.</p>}
    <div className="live-grid">
      {resource.data?.map((stream) => {
        const preview = <>
          <div className="default-thumbnail">
            <span aria-hidden="true">Mitemau</span>
            <span className="live-badge">Live</span>
          </div>
          <h2>{stream.title}</h2>
        </>;
        return <article className="live-card" key={stream.id}>
          {stream.watchAccountId
            ? <AppLink className="live-card-link" href={`/${stream.watchAccountId}`}>{preview}</AppLink>
            : <div>{preview}<p className="stream-unavailable" role="status">No participants are available.</p></div>}
          <AppLink className="user-link" href={`/${stream.creatorId}`}>Creator {stream.creatorId.slice(0, 12)}</AppLink>
        </article>;
      })}
    </div>
  </main>;
}

function StreamPlayback({ stream, accountID, profileName }: { stream: PublicStream; accountID: string; profileName: string }) {
  const resource = usePublicResource<StreamDetail>(`/api/public/streams/${stream.id}`);
  return <div className="public-watch" data-stream-id={stream.id}>
    <header className="page-heading">
      {!resource.unavailable && <span className="live-badge">Live</span>}
      <h1>{resource.data?.stream.title ?? stream.title}</h1>
      <p className="account-id">Watching from {profileName}’s page</p>
      <ReportUser targetId={accountID} streamId={stream.id} streamTitle={stream.title} />
    </header>
    {resource.loading && !resource.data && <p className="public-notice" role="status">Loading stream…</p>}
    <LoadError message={resource.error} reload={resource.reload} />
    {resource.unavailable && <div className="public-notice">
      <p role="status">This stream is unavailable or has ended.</p>
      <AppLink href="/">Browse live streams</AppLink>
    </div>}
    {resource.data && <div className="watch-layout">
      <div className="watch-media">
        {resource.data.participants.length === 0 && <p className="public-notice" role="status">No participants are available.</p>}
        {resource.data.participants.map((participant) => <section className="participant" key={participant.accountId}>
          <h2><AppLink href={`/${participant.accountId}`}>Participant {participant.accountId.slice(0, 12)}</AppLink></h2>
          {participant.namespace
            ? <CameraViewer key={participant.namespace} initialNamespace={participant.namespace} />
            : <p role="status">{participant.publishingState === "disconnected" ? "Connection lost. Waiting for the participant to reconnect." : "Not sharing"}</p>}
        </section>)}
      </div>
      <LiveComments streamID={stream.id} />
    </div>}
  </div>;
}

export function UserPage({ accountID }: { accountID: string }) {
  const resource = usePublicResource<UserStream>(`/api/public/${accountID.length === 64 ? "users" : "profiles"}/${accountID}`);
  const profile = resource.data;
  const profileName = profile?.displayName || profile?.displayId || "User";
  return <main className="public-page">
    {!profile?.stream && <h1>{profileName}</h1>}
    {profile?.displayId && <p>@{profile.displayId}</p>}
    {resource.loading && !resource.data && <p className="public-notice" role="status">Loading user stream…</p>}
    <LoadError message={resource.error} reload={resource.reload} />
    {resource.unavailable && <p role="status">User not found.</p>}
    {resource.data && <AppLink href={`/broadcasting/invitations/${resource.data.accountId}`}>Invite to a stream</AppLink>}
    {resource.data?.stream
      ? <StreamPlayback key={resource.data.stream.id} stream={resource.data.stream} accountID={resource.data.accountId} profileName={profileName} />
      : resource.data && <div className="public-notice">
        <p role="status">Offline</p>
        <AppLink href="/">Browse live streams</AppLink>
        <ReportUser targetId={resource.data.accountId} />
      </div>}
  </main>;
}
