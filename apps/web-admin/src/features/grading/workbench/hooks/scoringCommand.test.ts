import { expect, it, vi } from "vitest";
import { submitScoringCommand } from "./scoringCommand";
import type { startScoringRun } from "../../../../api/review";
import { getBusinessCommandSession, setBusinessCommandScope } from "../../../../api/businessCommand";

const run = {
  id: "run-1", tenant_id: "tenant", exam_id: "exam-1", idempotency_key: "command", status: "needs_review",
  started_by: "actor", created_at: "", updated_at: "", total_count: 100, queued_count: 0,
  auto_confirmed_count: 0, human_confirmed_count: 0, review_count: 100, failed_count: 0
} satisfies Awaited<ReturnType<typeof startScoringRun>>["scoring_run"];
function fixture() {
  const values = new Map<string, string>();
  const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value); }, removeItem: (key: string) => { values.delete(key); } };
  return { values, options: { examId: "exam-1", storageKey: "tenant:actor:exam-1", storage, ready: vi.fn(async () => true), pending: vi.fn() } };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

function guardedFixture() {
  const { options, values } = fixture();
  setBusinessCommandScope("tenant", "actor");
  const expected = getBusinessCommandSession();
  return { values, options: { ...options, isCurrent: () => getBusinessCommandSession() === expected } };
}

it("keeps the original command after an uncertain response and reload, without rerunning readiness", async () => {
  const { options, values } = fixture();
  const start = vi.fn(async (_exam: string, id: string) => {
    expect(values.get(options.storageKey)).toBe(id);
    throw new Error("503 after commit");
  });
  const recover = vi.fn(async (_exam: string, id: string) => ({ command_id: id, status: "not_accepted" as const }));
  await expect(submitScoringCommand(options, { start, recover })).rejects.toThrow("503");
  const id = values.get(options.storageKey);
  await expect(submitScoringCommand({ ...options }, { start, recover })).rejects.toThrow("503");
  expect(start.mock.calls.map((call) => call[1])).toEqual([id, id]);
  expect(options.ready).toHaveBeenCalledTimes(1);
});

it("recovers committed creation without creating again, then gives a new explicit operation a new ID", async () => {
  const { options, values } = fixture();
  values.set(options.storageKey, "old-command");
  const start = vi.fn(async () => ({ scoring_run: run }));
  const recover = vi.fn(async (_exam: string, id: string) => ({ command_id: id, status: "succeeded" as const, scoring_run: run }));
  expect(await submitScoringCommand(options, { start, recover })).toBe(true);
  expect(start).not.toHaveBeenCalled();
  expect(values.size).toBe(0);
  await submitScoringCommand(options, { start, recover });
  expect(start).toHaveBeenCalledTimes(1);
  expect(options.pending.mock.calls.some(([id]) => id !== null && id !== "old-command")).toBe(true);
});

it("does not send a command when persistence fails", async () => {
  const { options } = fixture();
  options.storage.setItem = () => { throw new Error("storage full"); };
  const start = vi.fn(async () => ({ scoring_run: run }));
  const recover = vi.fn(async (_exam: string, id: string) => ({ command_id: id, status: "not_accepted" as const }));
  await expect(submitScoringCommand(options, { start, recover })).rejects.toThrow("storage full");
  expect(start).not.toHaveBeenCalled();
});

it("does not persist or start a new command when the session changes during readiness", async () => {
  const { options, values } = guardedFixture();
  const readiness = deferred<boolean>();
  const start = vi.fn(async () => ({ scoring_run: run }));
  const recover = vi.fn(async (_exam: string, id: string) => ({ command_id: id, status: "not_accepted" as const }));
  const pending = submitScoringCommand({ ...options, ready: () => readiness.promise }, { start, recover });
  setBusinessCommandScope("tenant", "another-actor");
  readiness.resolve(true);
  expect(await pending).toBe(false);
  expect(values.size).toBe(0);
  expect(start).not.toHaveBeenCalled();
  expect(options.pending).not.toHaveBeenCalled();
});

it.each(["not_accepted", "succeeded"] as const)("keeps the original command without starting or cleanup when a late %s recovery belongs to an old session", async status => {
  const { options, values } = guardedFixture();
  values.set(options.storageKey, "old-command");
  const response = deferred<Awaited<ReturnType<typeof import("../../../../api/review").recoverScoringCommand>>>();
  const start = vi.fn(async () => ({ scoring_run: run }));
  const recover = vi.fn(() => response.promise);
  const pending = submitScoringCommand(options, { start, recover });
  // 即使同账号退出后重登，旧回执也不能在新会话下重新发起评分或清除待恢复事实。
  setBusinessCommandScope("", "");
  setBusinessCommandScope("tenant", "actor");
  response.resolve({ command_id: "old-command", status, ...(status === "succeeded" ? { scoring_run: run } : {}) });
  expect(await pending).toBe(false);
  expect(start).not.toHaveBeenCalled();
  expect(options.ready).not.toHaveBeenCalled();
  expect(values.get(options.storageKey)).toBe("old-command");
  expect(options.pending).not.toHaveBeenCalled();
});

it("keeps the persisted command after an in-flight start succeeds for a previous session", async () => {
  const { options, values } = guardedFixture();
  const response = deferred<{ scoring_run: typeof run }>();
  const start = vi.fn(() => response.promise);
  const recover = vi.fn(async (_exam: string, id: string) => ({ command_id: id, status: "not_accepted" as const }));
  const pending = submitScoringCommand(options, { start, recover });
  await Promise.resolve();
  const original = values.get(options.storageKey);
  expect(start).toHaveBeenCalledExactlyOnceWith("exam-1", original);
  setBusinessCommandScope("another-tenant", "actor");
  response.resolve({ scoring_run: run });
  expect(await pending).toBe(false);
  expect(values.get(options.storageKey)).toBe(original);
  expect(options.pending.mock.calls).toEqual([[original]]);
});

it("does not query or mutate when the caller scope has already expired", async () => {
  const { options, values } = guardedFixture();
  values.set(options.storageKey, "old-command");
  setBusinessCommandScope("tenant", "another-actor");
  const start = vi.fn(async () => ({ scoring_run: run }));
  const recover = vi.fn(async (_exam: string, id: string) => ({ command_id: id, status: "not_accepted" as const }));
  expect(await submitScoringCommand(options, { start, recover })).toBe(false);
  expect(recover).not.toHaveBeenCalled();
  expect(start).not.toHaveBeenCalled();
  expect(values.get(options.storageKey)).toBe("old-command");
});
