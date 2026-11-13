import './App.css'
import { useEffect, useState } from 'react'

type Verification = {
  email?: string
  verified: boolean
  expiresAt?: string
  codeExpired?: boolean
  retryAfterSeconds?: number
}

function App() {
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

  return (
    <main className="registration">
      <h1>Create an account</h1>
      {loading ? <p role="status">Loading…</p> : <>
        {verified ? <section aria-label="Email verification complete">
          <h2>Email verified</h2>
          <p>{verification?.email}</p>
          <p>Account setup is not complete. Your email verification is valid until{' '}
            {new Date(verification!.expiresAt!).toLocaleTimeString()}.</p>
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
        {busy && <p role="status">Please wait…</p>}
        {error && <p role="alert">{error}</p>}
      </>}
    </main>
  )
}

export default App
