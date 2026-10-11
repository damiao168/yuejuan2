import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DesktopApiClient } from "../api/client";
import type { AuthUser, OfflineGradeCommand } from "../types";
import { assertOfflineGradeCommandIdentity, syncOfflineGradeCommand } from "./offlineReviewCommand";

const api = vi.hoisted(() => ({ getCommand: vi.fn(), submit: vi.fn() }));
vi.mock("../api/review", () => ({ getReviewCommand: api.getCommand, submitHumanGrade: api.submit }));

const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
const command: OfflineGradeCommand = {
  id: "frozen-command", taskId: "task-1", server: client.url("/"), actorId: "teacher", tenantId: "tenant",
  payload: {
    expected_revision: 7, score: 8, rubric_selections: [{ point_id: "point-1", score: 2 }],
    comments: "原评语", private_note: "原备注", student_feedback: "原反馈", reason: "原原因"
  }
};
const identityMessage = "待确认评分属于其他服务器或账号，请切回原账号核对；当前账号不会提交它。";

describe("offline command identity boundaries", () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it.each([
    [{ id: "" }, true, []],
    [{ taskId: "other-task" }, true, []],
    [{ server: "https://other.example.edu/" }, true, ["url"]],
    [{}, false, ["url"]],
    [{ actorId: "other-teacher" }, true, ["url", "user.id"]],
    [{ tenantId: "other-tenant" }, true, ["url", "user.id", "user.tenant_id"]]
  ] as const)("keeps server and user field evaluation order for identity case %#", (patch, hasUser, expectedReads) => {
    const reads: string[] = [];
    vi.spyOn(client, "url").mockImplementation(() => { reads.push("url"); return command.server; });
    const user = {
      get id() { reads.push("user.id"); return "teacher"; },
      get tenant_id() { reads.push("user.tenant_id"); return "tenant"; }
    } as AuthUser;
    expect(() => assertOfflineGradeCommandIdentity({ ...command, ...patch }, command.taskId, client, hasUser ? user : null))
      .toThrow(identityMessage);
    expect(reads).toEqual(expectedReads);
  });

  // 缺失身份字段属于无效运行时输入；即使字段与空用户都缺失，也不能通过身份校验。
  it.each([
    { actorId: "teacher", tenantId: "tenant" },
    { actorId: "", tenantId: "" },
    { actorId: undefined, tenantId: undefined }
  ])("always rejects a null user for identity fields %#", (patch) => {
    const invalid = { ...command, ...patch } as OfflineGradeCommand;
    expect(() => assertOfflineGradeCommandIdentity(invalid, command.taskId, client, null)).toThrow(identityMessage);
  });

  it("keeps exact field comparison for a present user with empty identity fields", () => {
    const user = { id: "", tenant_id: "" } as AuthUser;
    expect(() => assertOfflineGradeCommandIdentity({ ...command, actorId: "", tenantId: "" }, command.taskId, client, user))
      .not.toThrow();
  });
});

describe("offline command receipt and session ordering", () => {
  beforeEach(() => {
    api.getCommand.mockReset();
    api.submit.mockReset().mockResolvedValue({});
  });

  it.each([
    [false, "not_accepted", ["assert", "conflict", "assert", "assert", "submit", "assert"], "synced", false],
    [true, "not_accepted", ["assert", "receipt", "assert", "conflict", "assert", "assert", "submit", "assert"], "synced", false],
    [true, "takeover_ready", ["assert", "receipt", "assert", "assert", "submit", "assert"], "synced", false],
    [true, "succeeded", ["assert", "receipt", "assert"], "synced", false],
    [true, "rejected", ["assert", "receipt", "assert"], "failed", true]
  ] as const)("keeps session checks and frozen submission order for recover %s / %s", async (recover, status, expectedCalls, outcome, resolved) => {
    const calls: string[] = [];
    api.getCommand.mockImplementation(async () => {
      calls.push("receipt");
      return { command_id: command.id, status, result: { task: { id: command.taskId } } };
    });
    api.submit.mockImplementation(async () => { calls.push("submit"); return {}; });
    const assertCurrent = vi.fn(() => { calls.push("assert"); });
    const detectConflict = vi.fn(async () => { calls.push("conflict"); return null; });
    const result = await syncOfflineGradeCommand(client, command, recover, assertCurrent, detectConflict);
    expect(result).toMatchObject({ status: outcome, resolved });
    expect(calls).toEqual(expectedCalls);
    if (api.submit.mock.calls.length) {
      expect(api.submit).toHaveBeenCalledOnce();
      expect(api.submit.mock.calls[0][2]).toBe(command.payload);
      expect(api.submit.mock.calls[0][3]).toBe(command.id);
    }
  });

  it("keeps empty optional receipt operation and target fields compatible", async () => {
    api.getCommand.mockResolvedValue({ command_id: command.id, operation: "", target_id: "", status: "not_accepted" });
    await expect(syncOfflineGradeCommand(client, command, true, vi.fn(), vi.fn(async () => null)))
      .resolves.toEqual({ status: "synced", message: "同步成功，服务端已接收人工评分。", resolved: false });
    expect(api.submit.mock.calls[0][2]).toBe(command.payload);
    expect(api.submit.mock.calls[0][3]).toBe(command.id);
  });

  it.each([
    ["processing", "原评分仍在处理中，请稍后继续确认；当前不会重复提交。"],
    ["unknown", "原评分结果暂时无法确认，请保留草稿并稍后继续确认。"]
  ])("keeps the exact %s receipt failure without running a preflight", async (status, message) => {
    api.getCommand.mockResolvedValue({ command_id: command.id, status });
    const assertCurrent = vi.fn();
    const detectConflict = vi.fn(async () => null);
    await expect(syncOfflineGradeCommand(client, command, true, assertCurrent, detectConflict)).rejects.toThrow(message);
    expect(assertCurrent).toHaveBeenCalledTimes(2);
    expect(detectConflict).not.toHaveBeenCalled();
    expect(api.submit).not.toHaveBeenCalled();
  });
});
