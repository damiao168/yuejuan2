import { beforeEach, describe, expect, it, vi } from "vitest";
import { DesktopApiClient } from "../api/client";
import type { AuthUser, OfflineGradeCommand } from "../types";
import { assertOfflineGradeCommandIdentity, syncOfflineGradeCommand } from "./offlineReviewCommand";

const mocks = vi.hoisted(() => ({ getCommand: vi.fn(), submit: vi.fn() }));
vi.mock("../api/review", () => ({ getReviewCommand: mocks.getCommand, submitHumanGrade: mocks.submit }));

describe("offline grade command recovery", () => {
  const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
  const user = { id: "teacher", tenant_id: "tenant" } as AuthUser;
  const command: OfflineGradeCommand = {
    id: "fixed-command", taskId: "task", server: client.url("/"), tenantId: "tenant", actorId: "teacher",
    payload: { expected_revision: 7, score: 8, rubric_selections: [{ point_id: "point", score: 2 }], comments: "原评语", private_note: "原备注", student_feedback: "原反馈", reason: "原原因" }
  };
  let assertCurrent: ReturnType<typeof vi.fn<() => void>>;
  let detectConflict: ReturnType<typeof vi.fn<() => Promise<string | null>>>;

  beforeEach(() => {
    mocks.getCommand.mockReset().mockResolvedValue({ command_id: command.id, status: "not_accepted" });
    mocks.submit.mockReset().mockResolvedValue({});
    assertCurrent = vi.fn();
    detectConflict = vi.fn(async () => null);
  });

  it.each(["review.submit", "review.seed-submit"])("accepts a %s successful receipt without another submit or task read", async (operation) => {
    mocks.getCommand.mockResolvedValue({ command_id: command.id, operation, target_id: command.taskId, status: "succeeded", result: { task: { id: command.taskId, revision: 8, status: "completed" } } });
    expect(await syncOfflineGradeCommand(client, command, true, assertCurrent, detectConflict)).toMatchObject({ status: "synced" });
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(detectConflict).not.toHaveBeenCalled();
  });

  it("checks an unaccepted original command then sends the frozen payload with the same ID", async () => {
    await syncOfflineGradeCommand(client, command, true, assertCurrent, detectConflict);
    expect(mocks.getCommand).toHaveBeenCalledWith(client, command.id);
    expect(detectConflict).toHaveBeenCalledTimes(1);
    expect(mocks.submit).toHaveBeenCalledWith(client, command.taskId, command.payload, command.id);
    expect(mocks.getCommand.mock.invocationCallOrder[0]).toBeLessThan(detectConflict.mock.invocationCallOrder[0]);
    expect(detectConflict.mock.invocationCallOrder[0]).toBeLessThan(mocks.submit.mock.invocationCallOrder[0]);
  });

  it("takes over a server reservation without changing command ID or payload", async () => {
    mocks.getCommand.mockResolvedValue({ command_id: command.id, status: "takeover_ready", payload: command.payload });
    await syncOfflineGradeCommand(client, command, true, assertCurrent, detectConflict);
    expect(detectConflict).not.toHaveBeenCalled();
    expect(mocks.submit).toHaveBeenCalledWith(client, command.taskId, command.payload, command.id);
  });

  it.each(["processing", "unknown"])("never sends a grade while receipt status is %s", async (status) => {
    mocks.getCommand.mockResolvedValue({ command_id: command.id, status });
    await expect(syncOfflineGradeCommand(client, command, true, assertCurrent, detectConflict)).rejects.toThrow();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(detectConflict).not.toHaveBeenCalled();
  });

  it("unfreezes only after an authoritative rejected receipt", async () => {
    mocks.getCommand.mockResolvedValue({ command_id: command.id, status: "rejected", http_status: 422 });
    expect(await syncOfflineGradeCommand(client, command, true, assertCurrent, detectConflict)).toMatchObject({ status: "failed", resolved: true });
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(detectConflict).not.toHaveBeenCalled();
  });

  it.each([
    { command_id: "other-command", status: "not_accepted" },
    { command_id: command.id, status: "not_accepted", operation: "review.arbitrate" },
    { command_id: command.id, status: "not_accepted", target_id: "other-task" },
    { command_id: command.id, status: "succeeded", result: { task: { id: "other-task" } } },
    { command_id: command.id, status: "succeeded" }
  ])("rejects a mismatched or incomplete receipt without sending %#", async (receipt) => {
    mocks.getCommand.mockResolvedValue(receipt);
    await expect(syncOfflineGradeCommand(client, command, true, assertCurrent, detectConflict)).rejects.toThrow();
    expect(mocks.submit).not.toHaveBeenCalled();
  });

  it("does not send after the session changes while receipt recovery is awaiting", async () => {
    let finish!: (value: unknown) => void;
    let current = true;
    mocks.getCommand.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const operation = syncOfflineGradeCommand(client, command, true, () => { if (!current) throw new Error("会话已变化"); }, detectConflict);
    const rejected = expect(operation).rejects.toThrow("会话已变化");
    current = false;
    finish({ command_id: command.id, status: "not_accepted" });
    await rejected;
    expect(detectConflict).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
  });

  it("does not send after the session changes while preflight is awaiting", async () => {
    let finish!: (value: string | null) => void;
    let current = true;
    detectConflict.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const operation = syncOfflineGradeCommand(client, command, false, () => { if (!current) throw new Error("会话已变化"); }, detectConflict);
    const rejected = expect(operation).rejects.toThrow("会话已变化");
    current = false;
    finish(null);
    await rejected;
    expect(mocks.submit).not.toHaveBeenCalled();
  });

  it("retains a preflight conflict without issuing a command", async () => {
    detectConflict.mockResolvedValue("任务已变化");
    expect(await syncOfflineGradeCommand(client, command, false, assertCurrent, detectConflict)).toEqual({ status: "conflict", message: "任务已变化", resolved: true });
    expect(mocks.getCommand).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
  });

  it.each([
    { server: "https://other.example.edu/" }, { actorId: "other" }, { tenantId: "other" }, { taskId: "other" }, { id: "" }
  ])("rejects incompatible command identity %#", (patch) => {
    expect(() => assertOfflineGradeCommandIdentity({ ...command, ...patch }, command.taskId, client, user)).toThrow("属于其他服务器或账号");
  });
});
