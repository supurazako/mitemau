import { useState } from 'react'
import type { AccountSession } from './useSessionActivity'
import { recoveryPost, requirePasskeySupport } from './recoveryApi'

type Props = { busy: boolean, onBusy: (busy: boolean) => void, onRecovered: (account: AccountSession, codes: string[]) => void }
export default function AccountRecovery({ busy, onBusy, onRecovered }: Props) {
  const [open, setOpen] = useState(false)
  const [step, setStep] = useState<'offline' | 'email' | 'passkey'>('offline')
  const [email, setEmail] = useState('')
  const [offline, setOffline] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [until, setUntil] = useState('')

  async function run(action: () => Promise<void>) {
    onBusy(true)
    setError('')
    try { await action() }
    catch (err) {
      setError(err instanceof DOMException && err.name === 'NotAllowedError'
        ? 'Passkey registration was canceled or timed out. Retry before recovery verification expires.'
        : err instanceof DOMException && err.name === 'InvalidStateError'
          ? 'Choose a new device or Passkey provider for recovery.'
          : err instanceof Error ? err.message : 'Recovery failed. Please try again.')
    } finally { onBusy(false) }
  }
  async function send() {
    await recoveryPost('/api/recovery/email', { email, recoveryCode: offline })
    setCode('')
    setStep('email')
  }
  async function verify() {
    const result = await recoveryPost('/api/recovery/email/verify', { code })
    setUntil(result.expiresAt)
    setOffline('')
    setCode('')
    setStep('passkey')
  }
  async function register() {
    requirePasskeySupport()
    const options = await recoveryPost('/api/recovery/passkey/options', {})
    const credential = await navigator.credentials.create({ publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(options) })
    if (!(credential instanceof PublicKeyCredential)) throw new Error('No new Passkey was created. Try again.')
    const result = await recoveryPost('/api/recovery/passkey/finish', credential.toJSON())
    // Keep raw recovery codes out of the ordinary session state.
    onRecovered({ signedIn: result.signedIn, email: result.email, expiresAt: result.expiresAt, renewAfterSeconds: result.renewAfterSeconds }, result.recoveryCodes)
  }
  function reset() {
    setStep('offline'); setOffline(''); setCode(''); setUntil(''); setError('')
  }
  return <section aria-label="Account recovery">
    {!open ? <button type="button" disabled={busy} onClick={() => setOpen(true)}>Recover account</button> : <>
      <h2>Recover account</h2>
      {step === 'offline' && <form onSubmit={event => { event.preventDefault(); void run(send) }}>
        <label htmlFor="recovery-email">Account email address</label>
        <input id="recovery-email" type="email" required maxLength={254} disabled={busy} value={email} onChange={event => setEmail(event.target.value)} />
        <label htmlFor="offline-code">Offline recovery code</label>
        <input id="offline-code" type="text" required autoComplete="off" spellCheck={false} maxLength={64} disabled={busy} value={offline} onChange={event => setOffline(event.target.value)} />
        <button type="submit" disabled={busy}>Send recovery email</button>
      </form>}
      {step === 'email' && <>
        <p role="status">A recovery email was sent to {email}.</p>
        <form onSubmit={event => { event.preventDefault(); void run(verify) }}>
          <label htmlFor="recovery-email-code">Recovery email code</label>
          <input id="recovery-email-code" type="text" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" minLength={6} maxLength={6} required disabled={busy} value={code} onChange={event => setCode(event.target.value)} />
          <button type="submit" disabled={busy}>Verify recovery email</button>
        </form>
        <button type="button" disabled={busy} onClick={() => void run(send)}>Resend recovery email</button>
      </>}
      {step === 'passkey' && <>
        <p role="status">Recovery verified until {new Date(until).toLocaleTimeString()}. Register a new Passkey to finish.</p>
        <p>Finishing recovery will invalidate all previous Passkeys, login sessions, and recovery codes.</p>
        <button type="button" disabled={busy} onClick={() => void run(register)}>Register recovery Passkey</button>
      </>}
      <button type="button" disabled={busy} onClick={reset}>Start recovery again</button>
      <button type="button" disabled={busy} onClick={() => { reset(); setOpen(false) }}>Cancel recovery</button>
      {error && <p role="alert">{error}</p>}
    </>}
  </section>
}
