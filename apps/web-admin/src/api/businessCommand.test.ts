import { afterEach, expect, it, vi } from "vitest";
import { executeBusinessCommand, setBusinessCommandScope } from "./businessCommand";
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
