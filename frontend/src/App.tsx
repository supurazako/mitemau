import ReportComposer from "./ReportComposer";
import "./App.css";
import AccountPage from "./AccountPage";
import { useSyncExternalStore } from "react";
import { AppLink } from "./AppLink";
import { LiveList, UserPage } from "./PublicStreams";
import { ViewerProvider } from "./ViewerContext";

function subscribeToPath(onChange: () => void) {
  window.addEventListener("popstate", onChange);
  return () => window.removeEventListener("popstate", onChange);
}

export default function App() {
  const path = useSyncExternalStore(subscribeToPath, () => window.location.pathname).replace(/\/$/, "") || "/";
  const user = /^\/([a-f0-9]{64})$/i.exec(path) ?? /^\/users\/([a-z0-9][a-z0-9_-]{2,23})$/i.exec(path);

  const invitee = /^\/broadcasting\/invitations\/([a-f0-9]{64}|@[a-z0-9][a-z0-9_-]{2,23})$/i.exec(path)?.[1];
  const invitations = !!invitee || path === "/broadcasting/invitations";
  const broadcasting = invitations || path === "/broadcasting" || path === "/account/broadcasting" || path === "/broadcasting/playback";
  const accountPage = invitations ? "/broadcasting/invitations" : broadcasting ? "/broadcasting" : path === "/account" || path === "/sign-in" || path === "/sign-up" || path === "/recover-account" ? path : null;
  let page;
  if (path === "/") page = <LiveList />;
  else if (accountPage) page = <AccountPage page={accountPage} invitee={invitee} />;
  else if (user) page = <UserPage key={user[1]} accountID={user[1]} />;
  else page = <main className="public-page"><h1>Page not found</h1><AppLink href="/">Browse live streams</AppLink></main>;

  return <ViewerProvider>
    <header className="site-header">
      <AppLink href="/" className="site-name">Mitemau</AppLink>
      <nav aria-label="Main navigation">
        <AppLink href="/" aria-current={path === "/" ? "page" : undefined}>Live streams</AppLink>
        <AppLink href="/broadcasting" aria-current={broadcasting && !invitations ? "page" : undefined}>Broadcasting</AppLink>
        <AppLink href="/account" aria-current={path === "/account" ? "page" : undefined}>Account</AppLink>
      </nav>
    </header>
    <ReportComposer key={accountPage ? "/account" : path}>{page}</ReportComposer>
  </ViewerProvider>;
}
