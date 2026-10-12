// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { ApiClientError } from "../../../api/client";
import type { ManagementExam } from "../../../api/exams";
import { ManagementExamArchiveDialog } from "./ManagementExamArchiveDialog";
import { managementCommandKey, type ManagementArchiveCommand } from "./managementCommand";

const mocks = vi.hoisted(() => ({ get: vi.fn(), check: vi.fn(), archive: vi.fn(), restore: vi.fn(), recover: vi.fn(), success: vi.fn() }));
vi.mock("../../../api/exams", async (original) => ({ ...await original<typeof import("../../../api/exams")>(), getManagementExam: mocks.get, checkManagementExamArchive: mocks.check, archiveManagementExam: mocks.archive, restoreManagementExam: mocks.restore, recoverManagementExamCommand: mocks.recover }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { success: mocks.success } }) },
  Modal: ({ open, children, footer }: { open: boolean; children: ReactNode; footer: ReactNode }) => open ? <div>{children}{footer}</div> : null,
  Button: ({ children, disabled, loading, onClick }: { children: ReactNode; disabled?: boolean; loading?: boolean; onClick: () => void }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
  Space: ({ children }: { children: ReactNode }) => <>{children}</>,
  Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => <div>{message}{description}</div>
}));
vi.mock("../../../components/PageState", () => ({ LoadingState: ({ label }: { label: string }) => <p>{label}</p>, ErrorState: ({ message }: { message: string }) => <p>{message}</p> }));

const exam: ManagementExam = { id: "session", kind: "session", name: "期中考试", school_id: "school", grade_id: "grade", exam_type: "midterm_exam", revision: 3,
  created_by: "actor", class_ids: ["class"], default_class_ids: ["class"], inherited_subject_ids: ["math"],
  exams: [{ id: "math", name: "数学", subject: "math", school_id: "school", tenant_id: "tenant", exam_type: "midterm_exam", total_score: 150, status: "draft", grading_mode: "ai_assisted", appeal_enabled: true, publish_policy: "manual_publish", created_by: "actor", class_ids: ["class"], revision: 2 }],
  progress: { status: "draft", label: "草稿", total_count: 1, ready_count: 0, published_count: 0, counts: { draft: 1 } }, scope_complete: true, can_manage: true, can_edit: true, can_archive: true, can_restore: false };
const key = managementCommandKey("tenant:actor", "session", "session", "archive");
const original: ManagementArchiveCommand = { version: 1, commandId: "original-command", kind: "session", examId: "session", action: "archive", input: { expected_revision: 1, expected_subject_revisions: { math: 1 } }, createdAt: "2026-10-04T00:00:00Z" };
let container: HTMLDivElement;
let root: Root;
let onSaved: Mock<() => Promise<void>>;
let onClose: Mock<() => void>;
beforeEach(() => {
  vi.clearAllMocks();
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  localStorage.clear();
  mocks.get.mockResolvedValue({ exam });
  mocks.check.mockResolvedValue({ check: { allowed: true, blockers: [] } });
  mocks.recover.mockResolvedValue({ command: { command_id: original.commandId, status: "not_accepted" } });
  mocks.archive.mockResolvedValue({ exam: { ...exam, archived_at: "2026-10-04T00:00:00Z" } });
  onSaved = vi.fn<() => Promise<void>>(async () => undefined); onClose = vi.fn<() => void>();
  container = document.createElement("div"); document.body.append(container); root = createRoot(container);
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); localStorage.clear(); });
async function mount(action: "archive" | "restore" = "archive") { await act(async () => root.render(<ManagementExamArchiveDialog selection={{ exam, action }} actorScope="tenant:actor" onClose={onClose} onSaved={onSaved} />)); }
async function click(label: string) {
  const button = [...container.querySelectorAll("button")].find((item) => item.textContent === label);
  if (!button) throw new Error(`Missing button ${label}`);
  await act(async () => button.click());
}

describe("整场归档持久恢复", () => {
  it("提交前持久保存载荷，409 未知结果继续保留并查询后复用原键及版本", async () => {
    mocks.archive.mockImplementationOnce(async () => {
      expect(JSON.parse(localStorage.getItem(key)!)).toMatchObject({ input: { expected_revision: 3, expected_subject_revisions: { math: 2 } } });
      throw new ApiClientError(409, "operation_outcome_unknown", "");
    });
    await mount(); await click("归档考试");
    const pending = JSON.parse(localStorage.getItem(key)!) as ManagementArchiveCommand;
    expect(pending.commandId).toBeTruthy();
    await click("检查并重试");
    expect(mocks.recover).toHaveBeenCalledWith(pending.commandId);
    expect(mocks.archive.mock.calls[1]).toEqual(mocks.archive.mock.calls[0]);
    expect(localStorage.getItem(key)).toBeNull();
    expect(onSaved).toHaveBeenCalledTimes(1);
  });

  it("重新打开时先恢复历史回执，成功只刷新当前考试，不重放已完成归档", async () => {
    localStorage.setItem(key, JSON.stringify(original));
    mocks.recover.mockResolvedValue({ command: { command_id: original.commandId, status: "succeeded", exam: { ...exam, archived_at: "2026-10-04T00:00:00Z" } } });
    await mount();
    expect(mocks.recover).toHaveBeenCalledWith(original.commandId);
    expect(mocks.archive).not.toHaveBeenCalled();
    expect(mocks.get).not.toHaveBeenCalled();
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem(key)).toBeNull();
  });

  it("恢复查询仍在处理中时，检查不会改用新输入或再次提交", async () => {
    localStorage.setItem(key, JSON.stringify(original));
    mocks.recover.mockResolvedValue({ command: { command_id: original.commandId, status: "processing" } });
    await mount(); await click("检查并重试");
    expect(mocks.archive).not.toHaveBeenCalled();
    expect(JSON.parse(localStorage.getItem(key)!)).toEqual(original);
  });

  it("确认尚未接收时使用历史输入，不能替换成弹窗读取的新版本", async () => {
    localStorage.setItem(key, JSON.stringify(original));
    await mount(); await click("检查并重试");
    expect(mocks.archive).toHaveBeenCalledWith({ kind: "session", id: "session" }, original.input, original.commandId);
  });

  it("恢复查询登录失效不删除未知命令，也不提交新的归档", async () => {
    localStorage.setItem(key, JSON.stringify(original));
    mocks.recover.mockRejectedValue(new ApiClientError(401, "unauthorized", ""));
    await mount(); await click("检查并重试");
    expect(mocks.archive).not.toHaveBeenCalled();
    expect(JSON.parse(localStorage.getItem(key)!)).toEqual(original);
  });

  it("反向历史回执成功后重新读取当前考试，归档采用当前版本", async () => {
    const restoreKey = managementCommandKey("tenant:actor", "session", "session", "restore");
    localStorage.setItem(restoreKey, JSON.stringify({ ...original, action: "restore" }));
    mocks.recover.mockResolvedValue({ command: { command_id: original.commandId, status: "succeeded" } });
    await mount();
    expect(localStorage.getItem(restoreKey)).toBeNull();
    expect(mocks.get).toHaveBeenCalledWith("session", "session");
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();
    await click("归档考试");
    expect(mocks.archive.mock.calls[0][1]).toEqual({ expected_revision: 3, expected_subject_revisions: { math: 2 } });
    expect(onSaved).toHaveBeenCalledTimes(2);
  });

  it("恢复动作只读取恢复权限，不执行归档条件检查或归档请求", async () => {
    mocks.get.mockResolvedValue({ exam: { ...exam, can_restore: true, can_archive: false } });
    mocks.restore.mockResolvedValue({ exam });
    await mount("restore");
    expect(mocks.check).not.toHaveBeenCalled();
    await click("恢复考试");
    expect(mocks.archive).not.toHaveBeenCalled();
    expect(mocks.restore).toHaveBeenCalledWith({ kind: "session", id: "session" }, { expected_revision: 3, expected_subject_revisions: { math: 2 } }, expect.any(String));
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
