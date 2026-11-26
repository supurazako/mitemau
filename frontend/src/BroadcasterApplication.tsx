import { CommentPermissionLookup } from "./CommentPermissionControl";
import StaffReports from "./StaffReports";
import ModerationActivity from "./ModerationActivity";
import { useEffect, useRef, useState } from "react";

type Access = { status: "not_applied" | "pending" | "approved"; staff: boolean; accountId: string };
type Application = { accountId: string; email: string };

export default function BroadcasterApplication() {
  const [access, setAccess] = useState<Access | null>(null);
  const [applications, setApplications] = useState<Application[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(true);
  const running = useRef(false);

  async function refresh(signal?: AbortSignal) {
    const response = await fetch("/api/account/broadcaster", { signal });
    if (!response.ok) throw new Error("Could not load broadcasting permission.");
    const next: Access = await response.json();
    if (signal?.aborted) return;
    setAccess(next);
    if (next.staff) {
      const list = await fetch("/api/staff/broadcasters", { signal });
      if (!list.ok) throw new Error("Could not load applications.");
      const pending: Application[] = await list.json();
      if (!signal?.aborted) setApplications(pending);
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    running.current = true;
    void Promise.resolve().then(() => refresh(controller.signal)).catch((cause) => {
      if (!controller.signal.aborted) setError(String(cause));
    }).finally(() => {
      if (!controller.signal.aborted) {
        running.current = false;
        setBusy(false);
      }
    });
    return () => controller.abort();
  }, []);

  async function perform(path?: string) {
    if (running.current) return;
    running.current = true;
    setBusy(true);
    setError("");
    try {
      if (path) {
        const response = await fetch(path, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: "{}",
        });
        if (!response.ok) throw new Error((await response.json()).error || "Request failed.");
      }
      await refresh();
    } catch (cause) {
      setError(String(cause));
    } finally {
      running.current = false;
      setBusy(false);
    }
  }

  return (
    <section>
      <h2>Broadcasting permission</h2>
      <button disabled={busy} onClick={() => void perform()}>Refresh status</button>
      {error && <p role="alert">{error}</p>}
      {access?.status === "not_applied" && (
        <button disabled={busy} onClick={() => void perform("/api/account/broadcaster")}>
          Apply to broadcast
        </button>
      )}
      {access?.status === "pending" && <p role="status">Application pending approval</p>}
      {access?.status === "approved" && <p role="status">Broadcasting permission granted</p>}
      {access?.staff && (
        <section>
          <h3>Pending applications</h3>
          {applications.length === 0 && <p>No pending applications.</p>}
          <ul>{applications.map((application) => (
            <li key={application.accountId}>
              {application.email}{" "}
              <button disabled={busy} onClick={() => void perform(
                `/api/staff/broadcasters/${application.accountId}/approve`,
              )}>Approve</button>
            </li>
          ))}</ul>
        </section>
      )}
      {access?.staff && <><StaffReports /><CommentPermissionLookup /><ModerationActivity /></>}
    </section>
  );
}
