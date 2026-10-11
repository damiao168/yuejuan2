import { describe, expect, it } from "vitest";
import { ApiClientError } from "../../../api/client";
import { isManagementCommandUncertain, loadManagementCommand, managementCommandKey, type ManagementArchiveCommand } from "./managementCommand";

describe("归档命令恢复边界", () => {
  it("账户、学校租户、整场类型及操作分别保存，互不复用未知命令", () => {
    const keys = [
      managementCommandKey("tenant-a:user", "session", "exam", "archive"),
      managementCommandKey("tenant-b:user", "session", "exam", "archive"),
      managementCommandKey("tenant-a:other", "session", "exam", "archive"),
      managementCommandKey("tenant-a:user", "standalone", "exam", "archive"),
      managementCommandKey("tenant-a:user", "session", "exam", "restore")
    ];
    expect(new Set(keys).size).toBe(5);
  });

  it("409 进行中及结果未知保留原命令，明确版本冲突拒绝后才可重新读取", () => {
    expect(isManagementCommandUncertain(new ApiClientError(409, "operation_in_progress", ""))).toBe(true);
    expect(isManagementCommandUncertain(new ApiClientError(409, "operation_outcome_unknown", ""))).toBe(true);
    expect(isManagementCommandUncertain(new ApiClientError(409, "revision_conflict", ""))).toBe(false);
    expect(isManagementCommandUncertain(new ApiClientError(503, "idempotency_persist_failed", ""))).toBe(true);
  });

  it("恢复保留原输入与学科版本，损坏记录不能静默重建另一命令", () => {
    const command: ManagementArchiveCommand = { version: 1, commandId: "original-command", kind: "session", examId: "session", action: "archive", input: { expected_revision: 3, expected_subject_revisions: { math: 2, physics: 4 } }, createdAt: "2026-10-04T00:00:00Z" };
    expect(loadManagementCommand({ getItem: () => JSON.stringify(command) }, "key")).toEqual(command);
    expect(() => loadManagementCommand({ getItem: () => "{}" }, "key")).toThrow("记录异常");
  });
});
