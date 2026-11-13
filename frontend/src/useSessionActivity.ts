import { useEffect } from 'react'

export type AccountSession = {
  signedIn: boolean
  email?: string
  expiresAt?: string
  renewAfterSeconds?: number
}

export function notifySessionChange() {
  if (typeof BroadcastChannel === 'undefined') return
  const channel = new BroadcastChannel('mitemau-session')
  channel.postMessage('changed')
  channel.close()
}

// MoQ playback/broadcast code can report ongoing media activity without making
// a request per frame. Native HTML media events are also captured below.
export function reportSessionActivity() {
  window.dispatchEvent(new Event('mitemau-session-activity'))
}

export function useSessionActivity(
  account: AccountSession,
  active: boolean,
  onSession: (session: AccountSession) => void,
) {
  const email = account.email
  const intervalSeconds = account.renewAfterSeconds ?? 1800
  useEffect(() => {
    if (!active) return
    const controller = new AbortController()
    let closed = false
    let inFlight = false
    let lastActivity = 0
    let sentActivity = 0
    let lastRenewed = 0
    const interval = Math.max(1000, intervalSeconds * 1000)

    async function renewIfNeeded() {
      if (closed || inFlight || lastActivity <= sentActivity || Date.now() - lastRenewed < interval) return
      inFlight = true
      const activity = lastActivity
      // Back off even on transport errors instead of issuing a request per event.
      lastRenewed = Date.now()
      try {
        const response = await fetch('/api/session/renew', {
          method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
          signal: controller.signal,
        })
        if (closed) return
        if (response.status === 401) {
          onSession({ signedIn: false })
          notifySessionChange()
          return
        }
        if (!response.ok) return
        const result: AccountSession = await response.json()
        if (!closed) {
          sentActivity = activity
          onSession(result)
          notifySessionChange()
        }
      } catch { /* Retry only if activity remains and the renewal interval has elapsed. */ }
      finally { inFlight = false }
    }

    function markActivity() {
      lastActivity = Date.now()
      void renewIfNeeded()
    }
    function mediaActivity(event: Event) {
      const media = event.target
      if (media instanceof HTMLMediaElement && !media.paused && !media.ended) markActivity()
    }
    // A deliberate page visit is activity. A hidden background mount is not.
    if (document.visibilityState === 'visible') markActivity()
    const events = ['pointerdown', 'pointermove', 'keydown', 'scroll'] as const
    for (const event of events) document.addEventListener(event, markActivity, { passive: true, capture: true })
    document.addEventListener('timeupdate', mediaActivity, true)
    document.addEventListener('play', mediaActivity, true)
    window.addEventListener('mitemau-session-activity', markActivity)
    const timer = window.setInterval(() => void renewIfNeeded(), Math.min(60_000, interval))
    return () => {
      closed = true
      controller.abort()
      window.clearInterval(timer)
      for (const event of events) document.removeEventListener(event, markActivity, true)
      document.removeEventListener('timeupdate', mediaActivity, true)
      document.removeEventListener('play', mediaActivity, true)
      window.removeEventListener('mitemau-session-activity', markActivity)
    }
  }, [active, email, intervalSeconds, onSession])
}
