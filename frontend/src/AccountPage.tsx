import AccountProfile from "./AccountProfile";
import { AppLink } from "./AppLink";
import "./App.css";
import BroadcasterApplication from "./BroadcasterApplication";
import BackupPasskey from "./BackupPasskey";
import AccountRecovery from "./AccountRecovery";
import ReplaceRecoveryCodes, { RecoveryCodesDisplay } from "./RecoveryCodes";
import StreamManager from "./StreamManager";
import { useEffect, useState } from "react";
import { useViewer } from "./lib/viewer";
import { notifySessionChange, useSessionActivity } from "./useSessionActivity";
import type { AccountSession } from "./useSessionActivity";

type Verification = {
  email?: string;
  verified: boolean;
  expiresAt?: string;
  codeExpired?: boolean;
  retryAfterSeconds?: number;
};

const headings = { "/account": "Account", "/broadcasting": "Broadcasting", "/broadcasting/invitations": "Broadcasting", "/sign-in": "Sign in", "/sign-up": "Create an account", "/recover-account": "Recover account" };

function AccountPage({ page, invitee }: { page: keyof typeof headings; invitee?: string }) {
  const viewer = useViewer();
  const invitationsPage = page === "/broadcasting/invitations";
  const broadcastingPage = page === "/broadcasting" || invitationsPage;
  const accountPage = page === "/account" || broadcastingPage;
  const [account, setAccount] = useState<AccountSession>({ signedIn: false });
  const [issuedCodes, setIssuedCodes] = useState<{
    email: string;
    codes: string[];
  } | null>(null);
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [verification, setVerification] = useState<Verification | null>(null);
  const [registrationReady, setRegistrationReady] = useState(false);
  const [sessionKnown, setSessionKnown] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [retryUntil, setRetryUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const controller = new AbortController();
    async function restore() {
      try {
        const sessionResponse = await fetch("/api/session", {
          signal: controller.signal,
        });
        if (!sessionResponse.ok)
          throw new Error(
            "Could not restore your session. Reload to try again.",
          );
        const restoredSession: AccountSession = await sessionResponse.json();
        if (!controller.signal.aborted) {
          setAccount(restoredSession);
          setSessionKnown(true);
        }
      } catch (err) {
        if (!controller.signal.aborted)
          setError(
            err instanceof Error
              ? err.message
              : "Could not connect to the API.",
          );
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    }
    void restore();
    return () => controller.abort();
  }, []);

  const signUpPage = page === "/sign-up";
  const hasSession = account.signedIn;
  useEffect(() => {
    if (!signUpPage || !sessionKnown || hasSession || registrationReady) return;
    const controller = new AbortController();
    async function restoreVerification() {
      try {
        const response = await fetch("/api/registration/email", { signal: controller.signal });
        if (!response.ok) throw new Error("Could not restore registration. Reload to try again.");
        const data: Verification = await response.json();
        if (controller.signal.aborted) return;
        setVerification(data);
        setEmail(data.email ?? "");
        setRetryUntil(Date.now() + (data.retryAfterSeconds ?? 0) * 1000);
      } catch (cause) {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause));
      } finally {
        if (!controller.signal.aborted) setRegistrationReady(true);
      }
    }
    void restoreVerification();
    return () => controller.abort();
  }, [signUpPage, sessionKnown, hasSession, registrationReady]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    async function refreshSession() {
      try {
        const response = await fetch("/api/session", {
          signal: controller.signal,
        });
        if (!response.ok) return;
        const result: AccountSession = await response.json();
        if (!controller.signal.aborted) {
          setSessionKnown(true);
          setAccount(result);
        }
      } catch {
        /* Keep the last known display when offline; the server checks authorization. */
      }
    }
    const onVisible = () => {
      if (document.visibilityState === "visible") void refreshSession();
    };
    window.addEventListener("focus", onVisible);
    document.addEventListener("visibilitychange", onVisible);
    const channel =
      typeof BroadcastChannel === "undefined"
        ? null
        : new BroadcastChannel("mitemau-session");
    if (channel) channel.onmessage = () => void refreshSession();
    return () => {
      controller.abort();
      window.removeEventListener("focus", onVisible);
      document.removeEventListener("visibilitychange", onVisible);
      channel?.close();
    };
  }, []);

  const sessionExpired = Boolean(
    account.signedIn &&
    account.expiresAt &&
    Date.parse(account.expiresAt) <= now,
  );
  const signedIn = account.signedIn && !sessionExpired;
  useSessionActivity(account, signedIn, setAccount);

  const next = new URLSearchParams(window.location.search).get("next") ?? "";
  const returnPath = /^\/broadcasting(?:\/invitations(?:\/(?:[a-f0-9]{64}|@[a-z0-9][a-z0-9_-]{2,23}))?)?$/i.test(next)
    ? next : next === "broadcasting" ? "/broadcasting" : "/account";
  const protectedPath = invitee ? `/broadcasting/invitations/${invitee}` : page;
  const redirectTo = sessionKnown && signedIn !== accountPage
    ? signedIn ? returnPath : broadcastingPage ? `/sign-in?next=${encodeURIComponent(protectedPath)}` : "/sign-in"
    : null;
  useEffect(() => {
    if (!redirectTo) return;
    window.history.replaceState(null, "", redirectTo);
    window.dispatchEvent(new PopStateEvent("popstate"));
    window.scrollTo(0, 0);
  }, [redirectTo]);


  const remaining = Math.max(0, Math.ceil((retryUntil - now) / 1000));
  const expired = Boolean(
    verification?.expiresAt && Date.parse(verification.expiresAt) <= now,
  );
  const verified = Boolean(verification?.verified && !expired);
  const pending = Boolean(verification?.email && !verified);

  async function submit(path: string, body: object) {
    setBusy(true);
    setError("");
    try {
      const response = await fetch(path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const data = await response.json();
      if (!response.ok) {
        const retry = Number(response.headers.get("Retry-After"));
        if (retry > 0) setRetryUntil(Date.now() + retry * 1000);
        throw new Error(data.error || "The request failed. Please try again.");
      }
      setVerification(data);
      setEmail(data.email);
      setCode("");
      setNow(Date.now());
      setRetryUntil(Date.now() + (data.retryAfterSeconds ?? 0) * 1000);
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : "Could not connect to the API. Please try again.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function registerPasskey() {
    setBusy(true);
    setError("");
    try {
      if (
        !window.isSecureContext ||
        typeof PublicKeyCredential === "undefined" ||
        typeof PublicKeyCredential.parseCreationOptionsFromJSON !==
          "function" ||
        typeof PublicKeyCredential.prototype.toJSON !== "function"
      ) {
        throw new Error(
          "This browser does not support Passkey registration here. Use a current Chrome or Chromium browser on localhost or HTTPS.",
        );
      }
      const optionsResponse = await fetch("/api/registration/passkey/options", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      const options = await optionsResponse.json();
      if (!optionsResponse.ok)
        throw new Error(
          options.error || "Could not start Passkey registration.",
        );
      const publicKey =
        PublicKeyCredential.parseCreationOptionsFromJSON(options);
      const credential = await navigator.credentials.create({ publicKey });
      if (!(credential instanceof PublicKeyCredential))
        throw new Error("No Passkey was created. Please try again.");
      const response = await fetch("/api/registration/passkey/finish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(credential.toJSON()),
      });
      const result = await response.json();
      if (!response.ok)
        throw new Error(result.error || "Could not complete registration.");
      setAccount({
        signedIn: result.signedIn,
        email: result.email,
        expiresAt: result.expiresAt,
        renewAfterSeconds: result.renewAfterSeconds,
      });
      setIssuedCodes(
        result.recoveryCodes
          ? { email: result.email, codes: result.recoveryCodes }
          : null,
      );
      setVerification(null);
      notifySessionChange();
    } catch (err) {
      if (err instanceof DOMException && err.name === "NotAllowedError") {
        setError(
          "Passkey registration was canceled or timed out. You can try again while your email verification is valid.",
        );
      } else {
        setError(
          err instanceof Error
            ? err.message
            : "Could not register the Passkey. Please try again.",
        );
      }
    } finally {
      setBusy(false);
    }
  }

  async function signIn() {
    setBusy(true);
    setError("");
    try {
      if (
        !window.isSecureContext ||
        typeof PublicKeyCredential === "undefined" ||
        typeof PublicKeyCredential.parseRequestOptionsFromJSON !== "function" ||
        typeof PublicKeyCredential.prototype.toJSON !== "function"
      ) {
        throw new Error(
          "This browser does not support Passkey sign-in here. Use a current Chrome or Chromium browser on localhost or HTTPS.",
        );
      }
      const response = await fetch("/api/login/passkey/options", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      const options = await response.json();
      if (!response.ok)
        throw new Error(options.error || "Could not start sign-in.");
      const publicKey =
        PublicKeyCredential.parseRequestOptionsFromJSON(options);
      const credential = await navigator.credentials.get({ publicKey });
      if (!(credential instanceof PublicKeyCredential))
        throw new Error("No Passkey was selected. Please try again.");
      const finish = await fetch("/api/login/passkey/finish", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(credential.toJSON()),
      });
      const result = await finish.json();
      if (!finish.ok)
        throw new Error(result.error || "Could not complete sign-in.");
      setAccount({
        signedIn: result.signedIn,
        email: result.email,
        expiresAt: result.expiresAt,
        renewAfterSeconds: result.renewAfterSeconds,
      });
      setIssuedCodes(
        result.recoveryCodes
          ? { email: result.email, codes: result.recoveryCodes }
          : null,
      );
      setVerification(null);
      setNow(Date.now());
      notifySessionChange();
    } catch (err) {
      if (err instanceof DOMException && err.name === "NotAllowedError") {
        setError(
          "Passkey sign-in was canceled or timed out. You can try again.",
        );
      } else {
        setError(
          err instanceof Error
            ? err.message
            : "Could not sign in. Please try again.",
        );
      }
    } finally {
      setBusy(false);
    }
  }

  async function signOut() {
    setBusy(true);
    setError("");
    try {
      const response = await fetch("/api/logout", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      const result = await response.json();
      if (!response.ok)
        throw new Error(
          result.error || "Could not sign out. Please try again.",
        );
      setAccount({ signedIn: false });
      setIssuedCodes(null);
      setVerification(null);
      setEmail("");
      setCode("");
      notifySessionChange();
    } catch (err) {
      setError(
        err instanceof Error
          ? err.message
          : "Could not sign out. Please try again.",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className={`registration${broadcastingPage && signedIn ? " account-studio" : ""}`}>
      <header className="page-heading">
        <h1>{headings[page]}</h1>
        {error && !(page === "/account" && signedIn) && <p role="alert">{error}</p>}
      </header>
      {accountPage && signedIn && (
        // Keep the sending session mounted when switching between settings pages.
        <div className="broadcasting-layout" hidden={!broadcastingPage} key={account.email}>
          <nav className="broadcasting-sidebar" aria-label="Broadcasting navigation">
            <AppLink href="/broadcasting" aria-current={!invitationsPage ? "page" : undefined}>Studio</AppLink>
            {viewer.canBroadcast && <AppLink href="/broadcasting/invitations" aria-current={invitationsPage ? "page" : undefined}>Invitations</AppLink>}
          </nav>
          <div className="broadcasting-content">
            <div hidden={invitationsPage}><BroadcasterApplication /></div>
            <StreamManager invitationsPage={invitationsPage} invitee={invitee} />
          </div>
        </div>
      )}
      {loading || redirectTo ? (
        <p role="status">Loading…</p>
      ) : (
        <>
          {signedIn ? (
            <section hidden={broadcastingPage} className="account-details" aria-label="Signed-in account">
              <h2>Signed in</h2>
              <p>You are signed in as {account.email}.</p>
              {!broadcastingPage && <AccountProfile key={account.email} />}
              <RecoveryCodesDisplay
                codes={
                  issuedCodes && issuedCodes.email === account.email
                    ? issuedCodes.codes
                    : []
                }
                onDismiss={() => setIssuedCodes(null)}
              />
              <ReplaceRecoveryCodes
                key={"codes-" + account.email}
                busy={busy}
                onBusy={setBusy}
                onCodes={(codes) =>
                  setIssuedCodes({ email: account.email!, codes })
                }
              />
              <BackupPasskey key={account.email} busy={busy} onBusy={setBusy} />
              <footer className="account-sign-out">
                <button type="button" disabled={busy} onClick={() => void signOut()}>
                  Sign out
                </button>
                {error && <p role="alert">{error}</p>}
              </footer>
            </section>
          ) : page === "/sign-in" ? (
            <>
              <section aria-label="Sign in">
                {sessionExpired && (
                  <p role="status">
                    Your session expired. Please sign in again.
                  </p>
                )}
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => void signIn()}
                >
                  Sign in with a Passkey
                </button>
              </section>
              <nav className="auth-links" aria-label="Other account options">
                <AppLink href="/sign-up">Create an account</AppLink>
                <AppLink href="/recover-account">Recover account</AppLink>
              </nav>
            </>
          ) : page === "/recover-account" ? (
            <>
              <AccountRecovery
                busy={busy}
                onBusy={setBusy}
                onRecovered={(result, codes) => {
                  setAccount(result);
                  setIssuedCodes({ email: result.email!, codes });
                  setVerification(null);
                  setError("");
                  setNow(Date.now());
                  notifySessionChange();
                }}
              />
              <AppLink href="/sign-in">Back to sign in</AppLink>
            </>
          ) : signUpPage ? (
            <>
              {!registrationReady && sessionKnown ? <p role="status">Loading registration…</p> : verified ? (
                <section aria-label="Email verification complete">
                  <h2>Email verified</h2>
                  <p>{verification?.email}</p>
                  <p>
                    Account setup is not complete. Your email verification is
                    valid until{" "}
                    {new Date(verification!.expiresAt!).toLocaleTimeString()}.
                  </p>
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => void registerPasskey()}
                  >
                    Register a Passkey
                  </button>
                </section>
              ) : (
                <>
                  <form
                    onSubmit={(event) => {
                      event.preventDefault();
                      void submit("/api/registration/email", { email });
                    }}
                  >
                    <label htmlFor="email">Email address</label>
                    <input
                      id="email"
                      type="email"
                      autoComplete="email"
                      maxLength={254}
                      required
                      value={email}
                      disabled={busy || pending}
                      onChange={(event) => setEmail(event.target.value)}
                    />
                    <button type="submit" disabled={busy || remaining > 0}>
                      {remaining > 0
                        ? `Resend available in ${remaining}s`
                        : pending
                          ? "Send a new code"
                          : "Send verification code"}
                    </button>
                  </form>
                  {pending && (
                    <>
                      <p role="status">
                        A verification code was sent to {verification?.email}.
                      </p>
                      {(verification?.codeExpired || expired) && (
                        <p>
                          The previous verification expired. Request a new code.
                        </p>
                      )}
                      <form
                        onSubmit={(event) => {
                          event.preventDefault();
                          void submit("/api/registration/email/verify", {
                            code,
                          });
                        }}
                      >
                        <label htmlFor="code">Verification code</label>
                        <input
                          id="code"
                          type="text"
                          inputMode="numeric"
                          autoComplete="one-time-code"
                          pattern="[0-9]{6}"
                          minLength={6}
                          maxLength={6}
                          required
                          value={code}
                          disabled={
                            busy || verification?.codeExpired || expired
                          }
                          onChange={(event) => setCode(event.target.value)}
                        />
                        <button
                          type="submit"
                          disabled={
                            busy || verification?.codeExpired || expired
                          }
                        >
                          Verify email
                        </button>
                      </form>
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          setVerification(null);
                          setCode("");
                          setError("");
                        }}
                      >
                        Use another email address
                      </button>
                    </>
                  )}
                </>
              )}
              <AppLink href="/sign-in">Back to sign in</AppLink>
            </>
          ) : null}
          {busy && <p role="status">Please wait…</p>}
        </>
      )}
    </main>
  );
}

export default AccountPage;
