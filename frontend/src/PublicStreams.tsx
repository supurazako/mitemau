import { useEffect, useState } from "react";
import CameraViewer from "./CameraViewer";
import { AppLink } from "./AppLink";

type PublicStream = { id: string; creatorId: string; title: string };
type LiveListing = PublicStream & { watchAccountId: string | null };
type Participant = { accountId: string; namespace: string | null };
type StreamDetail = { stream: PublicStream; participants: Participant[] };
type UserStream = { accountId: string; stream: PublicStream | null };

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
    <h1>Live streams</h1>
    {resource.loading && <p role="status">Loading live streams…</p>}
    <LoadError message={resource.error || (resource.unavailable ? "Live streams are unavailable." : "")} reload={resource.reload} />
    {resource.data?.length === 0 && <p role="status">No streams are live right now.</p>}
    <div className="live-grid">
      {resource.data?.map((stream) => {
        const preview = <>
          <div className="default-thumbnail" aria-hidden="true"><span>Mitemau</span></div>
          <h2>{stream.title}</h2>
        </>;
        return <article className="live-card" key={stream.id}>
          {stream.watchAccountId
            ? <AppLink href={`/${stream.watchAccountId}`}>{preview}</AppLink>
            : <div>{preview}<p className="stream-unavailable" role="status">No participants are available.</p></div>}
          <AppLink className="user-link" href={`/${stream.creatorId}`}>Creator {stream.creatorId.slice(0, 12)}</AppLink>
        </article>;
      })}
    </div>
  </main>;
}

function StreamPlayback({ streamID }: { streamID: string }) {
  const resource = usePublicResource<StreamDetail>(`/api/public/streams/${streamID}`);
  return <div className="public-watch" data-stream-id={streamID}>
    {resource.loading && <p role="status">Loading stream…</p>}
    <LoadError message={resource.error} reload={resource.reload} />
    {resource.unavailable && <p role="status">This stream is unavailable or has ended.</p>}
    {resource.data && <>
      <h2>{resource.data.stream.title}</h2>
      {resource.data.participants.length === 0 && <p role="status">No participants are available.</p>}
      {resource.data.participants.map((participant) => <section className="participant" key={participant.accountId}>
        <h3><AppLink href={`/${participant.accountId}`}>Participant {participant.accountId.slice(0, 12)}</AppLink></h3>
        {participant.namespace
          ? <CameraViewer key={participant.namespace} initialNamespace={participant.namespace} />
          : <p role="status">Not publishing</p>}
      </section>)}
    </>}
  </div>;
}

export function UserPage({ accountID }: { accountID: string }) {
  const resource = usePublicResource<UserStream>(`/api/public/users/${accountID}`);
  return <main className="public-page">
    <h1 className="account-id">User {accountID}</h1>
    {resource.loading && <p role="status">Loading user stream…</p>}
    <LoadError message={resource.error} reload={resource.reload} />
    {resource.unavailable && <p role="status">User not found.</p>}
    {resource.data && <>
      <p role="status">{resource.data.stream ? "Online" : "Offline"}</p>
      {resource.data.stream && <StreamPlayback key={resource.data.stream.id} streamID={resource.data.stream.id} />}
    </>}
  </main>;
}
