export async function recoveryPost(path: string, body: object) {
  const response = await fetch(path, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const data = await response.json()
  if (!response.ok) {
    const retry = Number(response.headers.get('Retry-After'))
    throw new Error((data.error || 'The request failed. Try again.') + (retry > 0 ? ` Try again in ${retry} seconds.` : ''))
  }
  return data
}

export function requirePasskeySupport() {
  if (!window.isSecureContext || typeof PublicKeyCredential === 'undefined' ||
      typeof PublicKeyCredential.parseRequestOptionsFromJSON !== 'function' ||
      typeof PublicKeyCredential.parseCreationOptionsFromJSON !== 'function' ||
      typeof PublicKeyCredential.prototype.toJSON !== 'function') {
    throw new Error('Use a current Chrome or Chromium browser on localhost or HTTPS for Passkeys.')
  }
}
