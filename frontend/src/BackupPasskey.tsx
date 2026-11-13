import { useState } from 'react'

type Props = {
  busy: boolean
  onBusy: (busy: boolean) => void
}

async function post(path: string, body: object) {
  const response = await fetch(path, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const data = await response.json()
  if (!response.ok) throw new Error(data.error || 'Could not add the Passkey. Start again.')
  return data
}

export default function BackupPasskey({ busy, onBusy }: Props) {
  const [options, setOptions] = useState<PublicKeyCredentialCreationOptionsJSON | null>(null)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')

  async function verify() {
    onBusy(true)
    setOptions(null)
    setError('')
    setMessage('')
    try {
      if (!window.isSecureContext || typeof PublicKeyCredential === 'undefined' ||
          typeof PublicKeyCredential.parseRequestOptionsFromJSON !== 'function' ||
          typeof PublicKeyCredential.parseCreationOptionsFromJSON !== 'function' ||
          typeof PublicKeyCredential.prototype.toJSON !== 'function') {
        throw new Error('Use a current Chrome or Chromium browser on localhost or HTTPS to add a Passkey.')
      }
      const request = await post('/api/account/passkeys/verify/options', {})
      const credential = await navigator.credentials.get({
        publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(request),
      })
      if (!(credential instanceof PublicKeyCredential)) throw new Error('No existing Passkey was selected. Start again.')
      const creation = await post('/api/account/passkeys/verify/finish', credential.toJSON())
      setOptions(creation)
      setMessage('Existing Passkey verified. Register another Passkey to complete setup.')
    } catch (err) {
      setError(err instanceof DOMException && err.name === 'NotAllowedError'
        ? 'Passkey verification was canceled or timed out. Start again when ready.'
        : err instanceof Error ? err.message : 'Could not verify the existing Passkey. Start again.')
    } finally { onBusy(false) }
  }

  async function register() {
    if (!options) return
    onBusy(true)
    setError('')
    setMessage('')
    try {
      const credential = await navigator.credentials.create({
        publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(options),
      })
      if (!(credential instanceof PublicKeyCredential)) throw new Error('No new Passkey was created. Start again.')
      await post('/api/account/passkeys/finish', credential.toJSON())
      setMessage('Additional Passkey registered. You can sign in using either Passkey.')
    } catch (err) {
      setError(err instanceof DOMException && err.name === 'NotAllowedError'
        ? 'New Passkey registration was canceled or timed out. Your existing Passkeys are unchanged. Start again when ready.'
        : err instanceof DOMException && err.name === 'InvalidStateError'
          ? 'This authenticator already has a Passkey for your account. Start again and choose another device or Passkey provider.'
          : err instanceof Error ? err.message : 'Could not register the new Passkey. Start again.')
    } finally {
      setOptions(null)
      onBusy(false)
    }
  }

  return <section aria-label="Backup Passkey">
    <h2>Add a backup Passkey</h2>
    <p>Verify an existing Passkey, then register another one. Choose another device or Passkey provider for a backup.</p>
    {options ? <>
      <button type="button" disabled={busy} onClick={() => void register()}>Register another Passkey</button>
      <button type="button" disabled={busy} onClick={() => { setOptions(null); setMessage(''); setError('') }}>Cancel adding a Passkey</button>
    </> : <button type="button" disabled={busy} onClick={() => void verify()}>Add a Passkey</button>}
    {message && <p role="status">{message}</p>}
    {error && <p role="alert">{error}</p>}
  </section>
}
