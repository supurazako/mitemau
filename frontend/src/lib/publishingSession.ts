export function createPublishingSession(streamID: string) {
  const sessionId = crypto.randomUUID().replaceAll("-", "");
  const path = `/api/streams/${streamID}/publishing`;

  async function update(stop: boolean, prepare = false) {
    const response = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sessionId, stop, prepare }),
      keepalive: stop,
    });
    if (!response.ok) throw new Error((await response.json()).error || "Could not update publishing.");
    return response.json() as Promise<{ namespace: string }>;
  }

  return {
    prepare: () => update(false, true),
    start: () => update(false),
    stop: async () => { await update(true); },
  };
}
