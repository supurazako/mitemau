import { useState } from 'react'
import { recoveryPost, requirePasskeySupport } from './recoveryApi'

export function RecoveryCodesDisplay({ codes, onDismiss }: { codes: string[], onDismiss: () => void }) {
  if (!codes.length) return null
  function download() {
    const url = URL.createObjectURL(new Blob(['Mitemau recovery codes\nKeep these offline. Each is single-use.\n\n' + codes.join('\n') + '\n'], { type: 'text/plain' }))
    const link = document.createElement('a')
    link.href = url
    link.download = 'mitemau-recovery-codes.txt'
    link.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return <section aria-label="Recovery codes">
    <h2>Save your recovery codes</h2>
    <p>These codes are shown once. Save them offline. Recovery requires one code and access to your email address. Previous codes are no longer valid.</p>
    <ul>{codes.map(code => <li key={code}><code>{code}</code></li>)}</ul>
    <button type="button" onClick={download}>Download recovery codes</button>
    <button type="button" onClick={onDismiss}>I have saved my codes</button>
  </section>
}

export default function ReplaceRecoveryCodes({ busy, onBusy, onCodes }: { busy: boolean, onBusy: (busy: boolean) => void, onCodes: (codes: string[]) => void }) {
  const [error, setError] = useState('')
  async function replace() {
    onBusy(true)
    setError('')
    try {
      requirePasskeySupport()
      const options = await recoveryPost('/api/account/recovery-codes/options', {})
      const credential = await navigator.credentials.get({ publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(options) })
      if (!(credential instanceof PublicKeyCredential)) throw new Error('No Passkey was selected. Try again.')
      const result = await recoveryPost('/api/account/recovery-codes/finish', credential.toJSON())
      onCodes(result.recoveryCodes)
    } catch (err) {
      setError(err instanceof DOMException && err.name === 'NotAllowedError'
        ? 'Passkey verification was canceled or timed out. Your recovery codes are unchanged.'
        : err instanceof Error ? err.message : 'Could not replace recovery codes. Try again.')
    } finally { onBusy(false) }
  }
  return <section aria-label="Replace recovery codes">
    <h2>Replace recovery codes</h2>
    <p>Verify a Passkey to issue ten new codes. All previous codes and pending recovery attempts will become invalid. Existing Passkeys and login sessions remain active.</p>
    <button type="button" disabled={busy} onClick={() => void replace()}>Replace recovery codes</button>
    {error && <p role="alert">{error}</p>}
  </section>
}
