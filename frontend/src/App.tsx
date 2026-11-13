import './App.css'
import BackupPasskey from './BackupPasskey'
import { useEffect, useState } from 'react'
import { notifySessionChange, useSessionActivity } from './useSessionActivity'
import type { AccountSession } from './useSessionActivity'

type Verification = {
  email?: string
  verified: boolean
  expiresAt?: string
  codeExpired?: boolean
  retryAfterSeconds?: number
}

function App() {
  const [account, setAccount] = useState<AccountSession>({ signedIn: false })
  const [email, setEmail] = useState('')
  const [code, setCode] = useState('')
  const [verification, setVerification] = useState<Verification | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [retryUntil, setRetryUntil] = useState(0)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const controller = new AbortController()
    async function restore() {
      try {
        const sessionResponse = await fetch('/api/session', { signal: controller.signal })
        if (!sessionResponse.ok) throw new Error('Could not restore your session. Reload to try again.')
        const restoredSession: AccountSession = await sessionResponse.json()
        if (restoredSession.signedIn) {
          if (!controller.signal.aborted) setAccount(restoredSession)
          return
        }
        const response = await fetch('/api/registration/email', { signal: controller.signal })
        if (!response.ok) throw new Error('Could not restore registration. Reload to try again.')
        const data: Verification = await response.json()
        if (!controller.signal.aborted) {
          setVerification(data)
          setEmail(data.email ?? '')
          setRetryUntil(Date.now() + (data.retryAfterSeconds ?? 0) * 1000)
        }
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : 'Could not connect to the API.')
      } finally {
        if (!controller.signal.aborted) setLoading(false)
      }
    }
    void restore()
    return () => controller.abort()
  }, [])

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    async function refreshSession() {
      try {
        const response = await fetch('/api/session', { signal: controller.signal })
        if (!response.ok) return
        const result: AccountSession = await response.json()
        if (!controller.signal.aborted) setAccount(result)
      } catch { /* Keep the last known display when offline; the server checks authorization. */ }
    }
    const onVisible = () => { if (document.visibilityState === 'visible') void refreshSession() }
    window.addEventListener('focus', onVisible)
    document.addEventListener('visibilitychange', onVisible)
    const channel = typeof BroadcastChannel === 'undefined' ? null : new BroadcastChannel('mitemau-session')
    if (channel) channel.onmessage = () => void refreshSession()
    return () => {
      controller.abort()
      window.removeEventListener('focus', onVisible)
      document.removeEventListener('visibilitychange', onVisible)
      channel?.close()
    }
  }, [])

  const sessionExpired = Boolean(account.signedIn && account.expiresAt && Date.parse(account.expiresAt) <= now)
  const signedIn = account.signedIn && !sessionExpired
  useSessionActivity(account, signedIn, setAccount)

  const remaining = Math.max(0, Math.ceil((retryUntil - now) / 1000))
  const expired = Boolean(verification?.expiresAt && Date.parse(verification.expiresAt) <= now)
  const verified = Boolean(verification?.verified && !expired)
  const pending = Boolean(verification?.email && !verified)

  async function submit(path: string, body: object) {
    setBusy(true)
    setError('')
    try {
      const response = await fetch(path, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      const data = await response.json()
      if (!response.ok) {
        const retry = Number(response.headers.get('Retry-After'))
        if (retry > 0) setRetryUntil(Date.now() + retry * 1000)
        throw new Error(data.error || 'The request failed. Please try again.')
      }
      setVerification(data)
      setEmail(data.email)
      setCode('')
      setNow(Date.now())
      setRetryUntil(Date.now() + (data.retryAfterSeconds ?? 0) * 1000)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not connect to the API. Please try again.')
    } finally {
      setBusy(false)
    }
  }

  async function registerPasskey() {
    setBusy(true)
    setError('')
    try {
      if (!window.isSecureContext || typeof PublicKeyCredential === 'undefined' ||
          typeof PublicKeyCredential.parseCreationOptionsFromJSON !== 'function' ||
          typeof PublicKeyCredential.prototype.toJSON !== 'function') {
        throw new Error('This browser does not support Passkey registration here. Use a current Chrome or Chromium browser on localhost or HTTPS.')
      }
      const optionsResponse = await fetch('/api/registration/passkey/options', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
      })
      const options = await optionsResponse.json()
      if (!optionsResponse.ok) throw new Error(options.error || 'Could not start Passkey registration.')
      const publicKey = PublicKeyCredential.parseCreationOptionsFromJSON(options)
      const credential = await navigator.credentials.create({ publicKey })
      if (!(credential instanceof PublicKeyCredential)) throw new Error('No Passkey was created. Please try again.')
      const response = await fetch('/api/registration/passkey/finish', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(credential.toJSON()),
      })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || 'Could not complete registration.')
      setAccount(result)
      setVerification(null)
      notifySessionChange()
    } catch (err) {
      if (err instanceof DOMException && err.name === 'NotAllowedError') {
        setError('Passkey registration was canceled or timed out. You can try again while your email verification is valid.')
      } else {
        setError(err instanceof Error ? err.message : 'Could not register the Passkey. Please try again.')
      }
    } finally {
      setBusy(false)
    }
  }

  async function signIn() {
    setBusy(true)
    setError('')
    try {
      if (!window.isSecureContext || typeof PublicKeyCredential === 'undefined' ||
          typeof PublicKeyCredential.parseRequestOptionsFromJSON !== 'function' ||
          typeof PublicKeyCredential.prototype.toJSON !== 'function') {
        throw new Error('This browser does not support Passkey sign-in here. Use a current Chrome or Chromium browser on localhost or HTTPS.')
      }
      const response = await fetch('/api/login/passkey/options', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
      })
      const options = await response.json()
      if (!response.ok) throw new Error(options.error || 'Could not start sign-in.')
      const publicKey = PublicKeyCredential.parseRequestOptionsFromJSON(options)
      const credential = await navigator.credentials.get({ publicKey })
      if (!(credential instanceof PublicKeyCredential)) throw new Error('No Passkey was selected. Please try again.')
      const finish = await fetch('/api/login/passkey/finish', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(credential.toJSON()),
      })
      const result = await finish.json()
      if (!finish.ok) throw new Error(result.error || 'Could not complete sign-in.')
      setAccount(result)
      setVerification(null)
      setNow(Date.now())
      notifySessionChange()
    } catch (err) {
      if (err instanceof DOMException && err.name === 'NotAllowedError') {
        setError('Passkey sign-in was canceled or timed out. You can try again.')
      } else {
        setError(err instanceof Error ? err.message : 'Could not sign in. Please try again.')
      }
    } finally { setBusy(false) }
  }

  async function signOut() {
    setBusy(true)
    setError('')
    try {
      const response = await fetch('/api/logout', {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
      })
      const result = await response.json()
      if (!response.ok) throw new Error(result.error || 'Could not sign out. Please try again.')
      setAccount({ signedIn: false })
      setVerification(null)
      setEmail('')
      setCode('')
      notifySessionChange()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not sign out. Please try again.')
    } finally { setBusy(false) }
  }

  return (
    <main className="registration">
      <h1>Mitemau</h1>
      {loading ? <p role="status">Loading…</p> : <>
        {signedIn ? <section aria-label="Signed-in account">
          <h2>Signed in</h2>
          <p>You are signed in as {account.email}.</p>
          <button type="button" disabled={busy} onClick={() => void signOut()}>Sign out</button>
          <BackupPasskey key={account.email} busy={busy} onBusy={setBusy} />
        </section> : <>
        <section aria-label="Sign in">
          <h2>Sign in</h2>
          {sessionExpired && <p role="status">Your session expired. Please sign in again.</p>}
          <button type="button" disabled={busy} onClick={() => void signIn()}>Sign in with a Passkey</button>
        </section>
        <h2>Create an account</h2>
        {verified ? <section aria-label="Email verification complete">
          <h2>Email verified</h2>
          <p>{verification?.email}</p>
          <p>Account setup is not complete. Your email verification is valid until{' '}
            {new Date(verification!.expiresAt!).toLocaleTimeString()}.</p>
          <button type="button" disabled={busy} onClick={() => void registerPasskey()}>Register a Passkey</button>
        </section> : <>
          <form onSubmit={(event) => {
            event.preventDefault()
            void submit('/api/registration/email', { email })
          }}>
            <label htmlFor="email">Email address</label>
            <input id="email" type="email" autoComplete="email" maxLength={254}
              required value={email} disabled={busy || pending}
              onChange={(event) => setEmail(event.target.value)} />
            <button type="submit" disabled={busy || remaining > 0}>
              {remaining > 0 ? `Resend available in ${remaining}s` : pending ? 'Send a new code' : 'Send verification code'}
            </button>
          </form>
          {pending && <>
            <p role="status">A verification code was sent to {verification?.email}.</p>
            {(verification?.codeExpired || expired) && <p>The previous verification expired. Request a new code.</p>}
            <form onSubmit={(event) => {
              event.preventDefault()
              void submit('/api/registration/email/verify', { code })
            }}>
              <label htmlFor="code">Verification code</label>
              <input id="code" type="text" inputMode="numeric" autoComplete="one-time-code"
                pattern="[0-9]{6}" minLength={6} maxLength={6} required value={code}
                disabled={busy || verification?.codeExpired || expired}
                onChange={(event) => setCode(event.target.value)} />
              <button type="submit" disabled={busy || verification?.codeExpired || expired}>Verify email</button>
            </form>
            <button type="button" disabled={busy} onClick={() => {
              setVerification(null)
              setCode('')
              setError('')
            }}>Use another email address</button>
          </>}
        </>}
        </>}
        {busy && <p role="status">Please wait…</p>}
        {error && <p role="alert">{error}</p>}
      </>}
    </main>
  )
}

export default App
