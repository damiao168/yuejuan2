import { afterEach, expect, it, vi } from "vitest";
import { captureBusinessCommandSession, executeBusinessCommand, resumePersistedBusinessCommand, setBusinessCommandScope } from "./businessCommand";
import { apiClient, ApiClientError } from "./client";

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });
function setup() {
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", { getItem: (k: string) => values.get(k) ?? null, setItem: (k: string, v: string) => values.set(k, v), removeItem: (k: string) => values.delete(k) });
  setBusinessCommandScope("tenant", "actor");
  return values;
}
it("coalesces double click without persisting or reconstructing the business payload", async () => {
  const values = setup();
  const send = vi.fn(async (_id: string, _input: unknown) => { throw new Error("503"); });
  const recover = (x: unknown) => x;
  const first = executeBusinessCommand("review.submit", "task", { score: 4, expected_revision: 2 }, send, recover);
  const duplicate = executeBusinessCommand("review.submit", "task", { score: 3, expected_revision: 3 }, send, recover);
  expect(duplicate).toBe(first);
  await expect(first).rejects.toThrow("503");
  const stored = JSON.parse([...values.values()][0]);
  expect(stored).toEqual({ id: expect.any(String) });
  vi.spyOn(apiClient, "request").mockResolvedValue({ command_id: stored.id, status: "not_accepted" });
  await expect(executeBusinessCommand("review.submit", "task", { score: 1, expected_revision: 9 }, send, recover)).rejects.toMatchObject({ code: "command_not_accepted" });
  expect(send.mock.calls).toEqual([[stored.id, { score: 4, expected_revision: 2 }]]);
  expect(values.size).toBe(0);
});
it("recovers committed result without submitting and permits a distinct next operation", async () => {
  const values = setup();
  values.set("business-command:tenant:actor:score.publish:exam", JSON.stringify({ id: "old", payload: { reason: "original" } }));
  vi.spyOn(apiClient, "request").mockResolvedValue({ command_id: "old", status: "succeeded", result: { status: "published" } });
  const send = vi.fn(async () => ({ status: "published" }));
  const recover = (x: unknown) => x as { status: string };
  expect(await executeBusinessCommand("score.publish", "exam", { reason: "edited" }, send, recover)).toEqual({ status: "published" });
  expect(send).not.toHaveBeenCalled();
  expect([...values.values()]).not.toContain(expect.stringContaining("original"));
  expect(values.size).toBe(0);
  await executeBusinessCommand("score.publish", "exam", { reason: "new" }, send, recover);
  expect(send).toHaveBeenCalledTimes(1);
});

it("does not resend a live processing or unknown command", async () => {
  const values = setup();
  const key = "business-command:tenant:actor:score.publish:exam";
  values.set(key, JSON.stringify({ id: "pending", payload: { reason: "original" } }));
  const request = vi.spyOn(apiClient, "request");
  request.mockResolvedValueOnce({ command_id: "pending", status: "processing" });
  const send = vi.fn();
  await expect(executeBusinessCommand("score.publish", "exam", { reason: "edited" }, send, value => value)).rejects.toMatchObject({ code: "operation_in_progress" });
  expect(send).not.toHaveBeenCalled();
  expect(values.has(key)).toBe(true);
  expect(JSON.parse(values.get(key)!)).toEqual({ id: "pending" });

  request.mockResolvedValueOnce({ command_id: "pending", status: "unknown", error_code: "business_receipt_missing" });
  await expect(executeBusinessCommand("score.publish", "exam", { reason: "edited" }, send, value => value)).rejects.toMatchObject({ code: "business_receipt_missing" });
  expect(send).not.toHaveBeenCalled();
  expect(values.has(key)).toBe(true);
});

it("resumes a stale reservation with the frozen command identity and payload", async () => {
  const values = setup();
  const key = "business-command:tenant:actor:score.publish:exam";
  values.set(key, JSON.stringify({ id: "stale-command", payload: { reason: "original" } }));
  vi.spyOn(apiClient, "request").mockResolvedValue({ command_id: "stale-command", status: "takeover_ready" });
  const send = vi.fn(async () => ({ status: "published" }));
  await executeBusinessCommand("score.publish", "exam", { reason: "edited" }, send, value => value as { status: string });
  expect(send).toHaveBeenCalledWith("stale-command", { reason: "original" });
  expect(values.has(key)).toBe(false);
});

it("uses the server-frozen payload for a stale payload-free command", async () => {
  const values = setup();
  const key = "business-command:tenant:actor:score.publish:exam";
  values.set(key, JSON.stringify({ id: "server-command" }));
  vi.spyOn(apiClient, "request").mockResolvedValue({ command_id: "server-command", status: "takeover_ready", payload: { reason: "server-frozen" } });
  const send = vi.fn(async () => ({ status: "published" }));
  await executeBusinessCommand("score.publish", "exam", { reason: "edited" }, send, value => value as { status: string });
  expect(send).toHaveBeenCalledWith("server-command", { reason: "server-frozen" });
  expect(values.has(key)).toBe(false);
});

it("clears a definitively rejected command so corrected input can use a new identity", async () => {
  const values = setup();
  const key = "business-command:tenant:actor:score.publish:exam";
  values.set(key, JSON.stringify({ id: "rejected", payload: { reason: "invalid" } }));
  vi.spyOn(apiClient, "request").mockResolvedValue({ command_id: "rejected", status: "rejected", http_status: 422, error_code: "publish_input_rejected" });
  const send = vi.fn();
  await expect(executeBusinessCommand("score.publish", "exam", { reason: "corrected" }, send, value => value)).rejects.toMatchObject({ code: "publish_input_rejected" });
  expect(send).not.toHaveBeenCalled();
  expect(values.has(key)).toBe(false);
});

it("clears a command rejected before execution by recent-auth so step-up can retry it", async () => {
  const values = setup();
  const send = vi.fn()
    .mockRejectedValueOnce(new ApiClientError(428, "recent_auth_required", "recent authentication required"))
    .mockResolvedValueOnce({ status: "published" });
  await expect(executeBusinessCommand("score.publish", "exam", { reason: "approved" }, send, value => value)).rejects.toMatchObject({ code: "recent_auth_required" });
  expect(values.size).toBe(0);
  await expect(executeBusinessCommand("score.publish", "exam", { reason: "approved" }, send, value => value)).resolves.toEqual({ status: "published" });
  expect(send).toHaveBeenCalledTimes(2);
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((onResolve, onReject) => { resolve = onResolve; reject = onReject; });
  return { promise, resolve, reject };
}

it.each(["takeover_ready", "not_accepted", "succeeded", "rejected"])("keeps the original receipt and never continues %s recovery after an account switch", async (status) => {
  const values = setup();
  const oldKey = "business-command:tenant:actor:score.publish:exam";
  const newKey = "business-command:tenant:another-actor:score.publish:exam";
  values.set(oldKey, JSON.stringify({ id: "old-command", payload: { reason: "old-payload" } }));
  const response = deferred<unknown>();
  vi.spyOn(apiClient, "request").mockImplementation(() => response.promise as Promise<never>);
  const send = vi.fn();
  const recover = vi.fn();
  const work = executeBusinessCommand("score.publish", "exam", { reason: "edited" }, send, recover);
  const result = expect(work).rejects.toMatchObject({ name: "BusinessCommandSessionChangedError" });
  setBusinessCommandScope("tenant", "another-actor");
  values.set(newKey, JSON.stringify({ id: "new-command" }));
  response.resolve({ command_id: "old-command", status, payload: { reason: "frozen" }, result: { published: true } });
  await result;
  expect(send).not.toHaveBeenCalled();
  expect(recover).not.toHaveBeenCalled();
  expect(JSON.parse(values.get(oldKey)!)).toEqual({ id: "old-command" });
  expect(JSON.parse(values.get(newKey)!)).toEqual({ id: "new-command" });
});

it.each(["success", "rejection"])("does not remove a later command when an old %s arrives after the same account logs in again", async (outcome) => {
  const values = setup();
  const key = "business-command:tenant:actor:review.submit:task";
  const response = deferred<{ score: number }>();
  const send = vi.fn(() => response.promise);
  const old = executeBusinessCommand("review.submit", "task", { score: 4 }, send, value => value);
  const result = expect(old).rejects.toMatchObject({ name: "BusinessCommandSessionChangedError" });
  setBusinessCommandScope("", "");
  setBusinessCommandScope("tenant", "actor");
  values.set(key, JSON.stringify({ id: "later-command" }));
  if (outcome === "success") response.resolve({ score: 4 });
  else response.reject(new ApiClientError(400, "bad_input", "rejected"));
  await result;
  expect(JSON.parse(values.get(key)!)).toEqual({ id: "later-command" });
  expect(send).toHaveBeenCalledTimes(1);
});

it("clears only the matching persisted command identity even without a session change", async () => {
  const values = setup();
  const key = "business-command:tenant:actor:review.submit:task";
  const response = deferred<{ score: number }>();
  const work = executeBusinessCommand("review.submit", "task", { score: 4 }, () => response.promise, value => value);
  values.set(key, JSON.stringify({ id: "another-command" }));
  response.resolve({ score: 4 });
  await expect(work).resolves.toEqual({ score: 4 });
  expect(JSON.parse(values.get(key)!)).toEqual({ id: "another-command" });
});

it("shares a pending recovery between the business page and the global recovery entry", async () => {
  const values = setup();
  values.set("business-command:tenant:actor:score.publish:exam", JSON.stringify({ id: "pending-command" }));
  const response = deferred<unknown>();
  const request = vi.spyOn(apiClient, "request").mockImplementation(() => response.promise as Promise<never>);
  const send = vi.fn(async () => ({ published: true }));
  const page = executeBusinessCommand("score.publish", "exam", { reason: "new" }, send, value => value);
  const global = resumePersistedBusinessCommand("score.publish", "exam", send, value => value, captureBusinessCommandSession());
  expect(global).toBe(page);
  response.resolve({ command_id: "pending-command", status: "takeover_ready", payload: { reason: "frozen" } });
  await expect(global).resolves.toEqual({ published: true });
  expect(request).toHaveBeenCalledTimes(1);
  expect(send).toHaveBeenCalledExactlyOnceWith("pending-command", { reason: "frozen" });
});

it("does not join the old session promise when the same account returns", async () => {
  const values = setup();
  const oldResponse = deferred<unknown>();
  const old = executeBusinessCommand("score.publish", "exam", null, () => oldResponse.promise, value => value);
  const result = expect(old).rejects.toMatchObject({ name: "BusinessCommandSessionChangedError" });
  const stored = JSON.parse([...values.values()][0]) as { id: string };
  setBusinessCommandScope("", "");
  setBusinessCommandScope("tenant", "actor");
  vi.spyOn(apiClient, "request").mockResolvedValue({ command_id: stored.id, status: "succeeded", result: { published: true } });
  const send = vi.fn();
  const current = executeBusinessCommand("score.publish", "exam", null, send, value => value);
  expect(current).not.toBe(old);
  await expect(current).resolves.toEqual({ published: true });
  oldResponse.resolve({ published: true });
  await result;
  expect(send).not.toHaveBeenCalled();
});
