import { expect, test } from "vitest";
import { newerComments, parseCommentSnapshot, untilAborted } from "../src/lib/comments";
import type { CommentSnapshot } from "../src/lib/comments";
const stream = "a1".repeat(16);
function snapshot(version: string, start: number, count = 50): CommentSnapshot {
  return { streamId: stream, version, namespace: `mitemau/streams/${stream}/comments/epoch`, comments: Array.from({ length: count }, (_, index) => ({
    id: (start + index).toString(16).padStart(32, "0"), sequence: String(start + index), accountId: "b1".repeat(32), body: `comment ${start + index}`, postedAt: "2026-11-26T00:00:00Z",
  })) };
}
test("a complete newer window replaces the old one without retaining older comments", () => {
  const old = parseCommentSnapshot(snapshot("50", 1), stream);
  const next = parseCommentSnapshot(snapshot("51", 2), stream);
  const current = newerComments(old, next);
  expect(current.comments).toHaveLength(50);
  expect(current.comments[0].body).toBe("comment 2");
  expect(newerComments(current, next).comments).toHaveLength(50);
  expect(newerComments(current, old)).toBe(current);
});
test("large update numbers are compared without Number precision loss", () => {
  const newer = snapshot("9007199254740993", 2);
  expect(newerComments(newer, snapshot("9007199254740992", 1))).toBe(newer);
});
test("malformed or cross-stream snapshots cannot overwrite the visible stream", () => {
  expect(() => parseCommentSnapshot(snapshot("1", 1), "c1".repeat(16))).toThrow();
  expect(() => parseCommentSnapshot(snapshot("1", 1, 51), stream)).toThrow();
  const repeated = snapshot("1", 1); repeated.comments[1] = repeated.comments[0];
  expect(() => parseCommentSnapshot(repeated, stream)).toThrow();
  const shuffled = snapshot("1", 1); shuffled.comments.reverse();
  expect(() => parseCommentSnapshot(shuffled, stream)).toThrow();
});

test("cancellation observes an already-created rejected operation", async () => {
  const controller = new AbortController(); controller.abort();
  await expect(untilAborted(Promise.reject(new Error("late connection failure")), controller.signal)).rejects.toMatchObject({ name: "AbortError" });
});
test("cancellation releases a wait on a pending transport operation", async () => {
  const controller = new AbortController();
  const result = untilAborted(new Promise<never>(() => {}), controller.signal);
  controller.abort();
  await expect(result).rejects.toMatchObject({ name: "AbortError" });
});
