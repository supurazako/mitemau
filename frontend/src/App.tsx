import "./App.css";
import AccountPage from "./AccountPage";
import { useSyncExternalStore } from "react";
import { AppLink } from "./AppLink";

function subscribeToPath(onChange: () => void) {
  window.addEventListener("popstate", onChange);
  return () => window.removeEventListener("popstate", onChange);
}
import { LiveList, UserPage } from "./PublicStreams";

export default function App() {
  const path = useSyncExternalStore(subscribeToPath, () => window.location.pathname).replace(/\/$/, "") || "/";
  const user = /^\/([a-f0-9]{64})$/i.exec(path);

  let page;
  if (path === "/") page = <LiveList />;
  else if (path === "/account") page = <AccountPage />;
  else if (user) page = <UserPage key={user[1]} accountID={user[1]} />;
  else page = <main className="public-page"><h1>Page not found</h1><AppLink href="/">Browse live streams</AppLink></main>;

  return <>
    <header className="site-header">
      <AppLink href="/" className="site-name">Mitemau</AppLink>
      <nav aria-label="Main navigation">
        <AppLink href="/" aria-current={path === "/" ? "page" : undefined}>Live streams</AppLink>
        <AppLink href="/account" aria-current={path === "/account" ? "page" : undefined}>Account &amp; broadcasting</AppLink>
      </nav>
    </header>
    {page}
  </>;
}
