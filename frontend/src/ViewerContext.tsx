import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { ViewerContext } from "./lib/viewer";
import type { ViewerAccess } from "./lib/viewer";

const signedOut: ViewerAccess = {
  ready: true,
  signedIn: false,
  staff: false,
  canBroadcast: false,
};

export function ViewerProvider({ children }: { children: ReactNode }) {
  const [viewer, setViewer] = useState<ViewerAccess>({
    ready: false,
    signedIn: false,
    staff: false,
    canBroadcast: false,
  });

  useEffect(() => {
    let active = true;
    let running = false;

    async function load() {
      if (running) return;
      running = true;
      try {
        const sessionResponse = await fetch("/api/session", {
          cache: "no-store",
        });
        if (!sessionResponse.ok) {
          if (active) setViewer(signedOut);
          return;
        }
        const session = await sessionResponse.json();
        if (!session.signedIn) {
          if (active) setViewer(signedOut);
          return;
        }

        const accessResponse = await fetch("/api/account/broadcaster", {
          cache: "no-store",
        });
        if (accessResponse.status === 401) {
          if (active) setViewer(signedOut);
          return;
        }
        const access = accessResponse.ok ? await accessResponse.json() : null;
        if (!active) return;
        setViewer({
          ready: true,
          signedIn: true,
          accountId: access?.accountId,
          broadcastingStatus: access?.status,
          staff: !!access?.staff,
          canBroadcast: access?.status === "approved",
        });
      } catch {
        // Treat an unavailable session as signed out. The server remains
        // authoritative for every action.
        if (active) setViewer(signedOut);
      } finally {
        running = false;
      }
    }

    void load();
    const reload = () => void load();
    const onVisible = () => {
      if (document.visibilityState === "visible") void load();
    };
    window.addEventListener("mitemau-session-change", reload);
    document.addEventListener("visibilitychange", onVisible);
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, 30_000);

    let channel: BroadcastChannel | undefined;
    if (typeof BroadcastChannel !== "undefined") {
      channel = new BroadcastChannel("mitemau-session");
      channel.onmessage = reload;
    }

    return () => {
      active = false;
      window.removeEventListener("mitemau-session-change", reload);
      document.removeEventListener("visibilitychange", onVisible);
      window.clearInterval(timer);
      channel?.close();
    };
  }, []);

  return (
    <ViewerContext.Provider value={viewer}>{children}</ViewerContext.Provider>
  );
}
