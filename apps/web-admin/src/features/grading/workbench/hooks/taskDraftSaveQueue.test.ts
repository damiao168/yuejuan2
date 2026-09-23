import { describe, expect, it } from "vitest";
import { TaskDraftSaveQueue } from "./taskDraftSaveQueue";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("task draft save queue", () => {
  it("keeps a late A response from changing B's revision and serializes later A edits", async () => {
    const queue = new TaskDraftSaveQueue();
    const firstA = deferred<number>();
    queue.observeRevision("user:A", 2);
    queue.observeRevision("user:B", 7);
    const calls: string[] = [];

    const a1 = queue.enqueue("user:A", async (revision) => {
      calls.push(`A1:${revision}`);
      return firstA.promise;
    });
    const a2 = queue.enqueue("user:A", async (revision) => {
      calls.push(`A2:${revision}`);
      return revision + 1;
    });
    const b = queue.enqueue("user:B", async (revision) => {
      calls.push(`B:${revision}`);
      return revision + 1;
    });

    await b;
    expect(calls).toEqual(["A1:2", "B:7"]);
    expect(queue.revision("user:B")).toBe(8);
    firstA.resolve(3);
    await Promise.all([a1, a2, queue.whenIdle("user:A")]);
    expect(calls).toEqual(["A1:2", "B:7", "A2:3"]);
    expect(queue.revision("user:A")).toBe(4);
    expect(queue.revision("user:B")).toBe(8);
  });

  it("continues queued edits after a failed save", async () => {
    const queue = new TaskDraftSaveQueue();
    queue.observeRevision("A", 1);
    const failed = queue.enqueue("A", async () => { throw new Error("network"); });
    const next = queue.enqueue("A", async (revision) => revision + 1);
    await expect(failed).rejects.toThrow("network");
    await expect(next).resolves.toBe(2);
  });

  it("keeps the newest same-task fallback until the queued edit is saved", async () => {
    const queue = new TaskDraftSaveQueue();
    const firstResponse = deferred<number>();
    queue.observeRevision("user:A", 4);
    let fallback: "first" | "second" | null = "first";
    const persisted: string[] = [];

    const first = queue.enqueue("user:A", async (revision) => {
      const captured = "first" as const;
      persisted.push(`${captured}:${revision}`);
      const nextRevision = await firstResponse.promise;
      if (fallback === captured) fallback = null;
      return nextRevision;
    });

    fallback = "second";
    const second = queue.enqueue("user:A", async (revision) => {
      const captured = fallback;
      persisted.push(`${captured}:${revision}`);
      if (fallback === captured) fallback = null;
      return revision + 1;
    });

    firstResponse.resolve(5);
    await Promise.all([first, second, queue.whenIdle("user:A")]);
    expect(persisted).toEqual(["first:4", "second:5"]);
    expect(fallback).toBeNull();
    expect(queue.revision("user:A")).toBe(6);
  });

  it("does not let task A's late completion remove task B's fallback", async () => {
    const queue = new TaskDraftSaveQueue();
    const lateA = deferred<number>();
    const fallbacks = new Map([["user:A", "A-local"], ["user:B", "B-local"]]);

    const a = queue.enqueue("user:A", async () => {
      const revision = await lateA.promise;
      if (fallbacks.get("user:A") === "A-local") fallbacks.delete("user:A");
      return revision;
    });
    await queue.enqueue("user:B", async () => 9);
    expect(fallbacks.get("user:B")).toBe("B-local");

    lateA.resolve(3);
    await a;
    expect(fallbacks.has("user:A")).toBe(false);
    expect(fallbacks.get("user:B")).toBe("B-local");
    expect(queue.revision("user:B")).toBe(9);
  });
});
