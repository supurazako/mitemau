export function createPublishingSession(streamID: string) {
  const sessionId = crypto.randomUUID().replaceAll("-", "");
  const path = `/api/streams/${streamID}/publishing`;

  async function update(stop: boolean, prepare = false, disconnected = false) {
    const response = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sessionId, stop, prepare, disconnected }),
      keepalive: stop,
    });
    if (!response.ok) throw new Error((await response.json()).error || "Could not update publishing.");
    return response.json() as Promise<{ namespace: string; token?: string }>;
  }

  return {
    async prepare() {
      const prepared = await update(false, true);
      if (!prepared.token) throw new Error("Publishing authorization missing.");
      return { namespace: prepared.namespace, token: prepared.token };
    },
    start: () => update(false),
    stop: async (disconnected = false) => { await update(true, false, disconnected); },
  };
}
