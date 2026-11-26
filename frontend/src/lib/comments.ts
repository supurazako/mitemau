export type LiveComment = { id: string; sequence: string; accountId: string; body: string; postedAt: string };
export type CommentSnapshot = { streamId: string; version: string; namespace: string | null; comments: LiveComment[] };
const integer = /^(0|[1-9][0-9]*)$/;
export function parseCommentSnapshot(value: unknown, streamID: string): CommentSnapshot {
  if (!value || typeof value !== "object") throw new Error("Invalid comment snapshot");
  const data = value as CommentSnapshot;
  if (data.streamId !== streamID || typeof data.version !== "string" || !integer.test(data.version)
    || !(data.namespace === null || typeof data.namespace === "string") || !Array.isArray(data.comments) || data.comments.length > 50) throw new Error("Invalid comment snapshot");
  const seen = new Set<string>();
  let previous = -1n;
  for (const item of data.comments) {
    if (!item || typeof item.id !== "string" || !/^[a-f0-9]{32}$/.test(item.id) || seen.has(item.id)
      || typeof item.accountId !== "string" || !/^[a-f0-9]{64}$/.test(item.accountId)
      || typeof item.sequence !== "string" || !integer.test(item.sequence) || BigInt(item.sequence) <= previous
      || typeof item.body !== "string" || Array.from(item.body).length > 500 || !item.body.trim()
      || typeof item.postedAt !== "string" || !Number.isFinite(Date.parse(item.postedAt))) throw new Error("Invalid comment snapshot");
    seen.add(item.id); previous = BigInt(item.sequence);
  }
  return data;
}
export function newerComments(current: CommentSnapshot | undefined, next: CommentSnapshot): CommentSnapshot {
  if (current && current.streamId === next.streamId && BigInt(current.version) > BigInt(next.version)) return current;
  return next;
}

export async function untilAborted<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) { void promise.catch(() => {}); throw new DOMException("Cancelled", "AbortError"); }
  let cancel!: () => void;
  const cancelled = new Promise<never>((_, reject) => { cancel = () => reject(new DOMException("Cancelled", "AbortError")); signal.addEventListener("abort", cancel, { once: true }); });
  try { return await Promise.race([promise, cancelled]); } finally { signal.removeEventListener("abort", cancel); }
}
