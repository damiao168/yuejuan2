// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DesktopApiClient } from "../api/client";
import type { AuthUser, OfflineDraftRecord, ReviewTask } from "../types";
import { OfflineWorkbench, type OfflineLeaveGuard } from "./OfflineWorkbench";

type LeaveDialog = { onOk: () => Promise<void>; footer: (node: ReactNode, buttons: { OkBtn: () => ReactNode; CancelBtn: () => ReactNode }) => ReactNode };
const mocks = vi.hoisted(() => ({ load: vi.fn(), list: vi.fn(), save: vi.fn(), confirm: vi.fn(), destroy: vi.fn(), update: vi.fn(), changeScore: undefined as undefined | ((value: number) => void) }));
vi.mock("../lib/durableStore", () => ({ hasDurableDesktopStore: () => true }));
vi.mock("../lib/offlineStore", () => ({ readOfflineDraftEnvelopes: () => [], loadOfflineDraft: mocks.load, listOfflineDraftEnvelopes: mocks.list, saveOfflineDraft: mocks.save, updateOfflineDraftStatus: vi.fn(), purgeExpiredOfflineDrafts: vi.fn() }));
vi.mock("../api/exams", () => ({ listExams: vi.fn() }));
vi.mock("../api/review", () => ({ getReviewTask: vi.fn(), getReviewTaskContext: vi.fn(), downloadReviewTaskImage: vi.fn(), listAiGrades: vi.fn(), listReviewTasks: vi.fn() }));
vi.mock("../api/submissions", () => ({ listAnswerSegments: vi.fn(), listOcrTasks: vi.fn(), listSubmissionPages: vi.fn() }));
vi.mock("../api/files", () => ({ downloadFileBlob: vi.fn() }));
vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  return {
    Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <aside>{message}{description}</aside>,
    Button: ({ children, onClick, disabled }: { children: ReactNode; onClick?: () => void; disabled?: boolean }) => <button onClick={onClick} disabled={disabled}>{children}</button>,
    Empty: Box, Form: Object.assign(Box, { Item: Box }), Input: Object.assign(Box, { Password: Box, TextArea: Box }),
    InputNumber: ({ value, onChange }: { value: number; onChange: (value: number) => void }) => { mocks.changeScore = onChange; return <span>{value}</span>; },
    List: Object.assign(() => null, { Item: Box }), Modal: { confirm: mocks.confirm }, Space: Box, Table: () => null, Tag: Box
  };
});

describe("离开确认的实际页脚", () => {
  const roots: Root[] = [];
  const hosts: HTMLDivElement[] = [];
  afterEach(async () => { await act(async () => roots.forEach((root) => root.unmount())); hosts.forEach((host) => host.remove()); vi.resetAllMocks(); });

  it("过期租约保存失败后原地续期，不重读覆盖输入，并用新租约保存当前评分", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const task = { id: "task", anonymous_code: "anon", status: "in_progress", exam_id: "exam" } as ReviewTask;
    const record: OfflineDraftRecord = { taskId: "task", anonymousCode: "anon", savedAt: "2026-10-08", expiresAt: "2026-10-15", syncStatus: "draft",
      packageSnapshot: { task, ocrText: "", aiGrades: [], warnings: [], downloadedAt: "2026-10-08" },
      draft: { taskId: "task", score: 1, rubricSelections: {}, comments: "", studentFeedback: "", privateNote: "", reason: "" } };
    mocks.load.mockResolvedValue(record); mocks.list.mockResolvedValue([record]); mocks.save.mockResolvedValue(undefined);
    const host = document.createElement("div"); document.body.append(host); hosts.push(host);
    const root = createRoot(host); roots.push(root);
    const user = { id: "teacher", tenant_id: "tenant" } as AuthUser;
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const onLog = vi.fn(async () => undefined);
    let guard: OfflineLeaveGuard | null = null;
    const registerLeaveGuard = (value: OfflineLeaveGuard | null) => { guard = value; };
    async function render(scope: string, token: string | null) {
      await act(async () => root.render(<OfflineWorkbench durableScopeKey={scope} client={client} token={token} user={user}
        isOnline onLog={onLog} registerLeaveGuard={registerLeaveGuard} initialTaskId="task" />));
    }
    const save = () => [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.textContent === "加密保存草稿")!.click();
    await render("expired-lease", null);
    await act(async () => mocks.changeScore!(2));
    mocks.load.mockRejectedValueOnce(new Error("本机离线授权已过期"));
    await act(async () => save());
    expect(host.textContent).toContain("尚未覆盖"); expect(guard!.shouldBlockClose()).toBe(true);
    const readsBeforeRenewal = mocks.load.mock.calls.length;
    await render("renewed-lease", "online-token");
    expect(mocks.load).toHaveBeenCalledTimes(readsBeforeRenewal); expect(mocks.confirm).not.toHaveBeenCalled();
    expect(guard!.shouldBlockClose()).toBe(true);
    await act(async () => save());
    expect(mocks.save).toHaveBeenCalledWith(expect.objectContaining({ draft: expect.objectContaining({ score: 2 }) }), "", "renewed-lease");
    expect(host.textContent).toContain("草稿已加密保存到本地"); expect(guard!.shouldBlockClose()).toBe(false);
  });

  it("页脚出现后开始写盘，已捕获的放弃按钮仍检查实时锁；失败后可明确放弃", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const task = { id: "task", anonymous_code: "anon", status: "in_progress", exam_id: "exam" } as ReviewTask;
    const record: OfflineDraftRecord = { taskId: "task", anonymousCode: "anon", savedAt: "2026-10-08", expiresAt: "2026-10-15", syncStatus: "draft",
      packageSnapshot: { task, ocrText: "", aiGrades: [], warnings: [], downloadedAt: "2026-10-08" },
      draft: { taskId: "task", score: 1, rubricSelections: {}, comments: "", studentFeedback: "", privateNote: "", reason: "" } };
    mocks.load.mockResolvedValue(record); mocks.list.mockResolvedValue([record]);
    mocks.confirm.mockReturnValue({ update: mocks.update, destroy: mocks.destroy });
    let rejectSave!: (error: Error) => void;
    mocks.save.mockImplementation(() => new Promise<void>((_resolve, reject) => { rejectSave = reject; }));
    const host = document.createElement("div"); const footerHost = document.createElement("div");
    document.body.append(host, footerHost); hosts.push(host, footerHost);
    const root = createRoot(host); const footerRoot = createRoot(footerHost); roots.push(root, footerRoot);
    let guard: OfflineLeaveGuard | null = null;
    await act(async () => root.render(<OfflineWorkbench durableScopeKey="scope" client={new DesktopApiClient({ baseUrl: "https://grading.example.edu", getToken: () => "token" })} token="token" user={{ id: "teacher", tenant_id: "tenant" } as AuthUser} isOnline onLog={vi.fn(async () => undefined)} registerLeaveGuard={(value) => { guard = value; }} initialTaskId="task" />));
    await act(async () => mocks.changeScore!(2));
    let leaving!: Promise<boolean>;
    await act(async () => { leaving = guard!.requestLeave(); });
    const dialog = mocks.confirm.mock.calls[0][0] as LeaveDialog;
    await act(async () => footerRoot.render(dialog.footer(null, { CancelBtn: () => <button>取消</button>, OkBtn: () => <button>保存后离开</button> })));
    const buttons = [...footerHost.querySelectorAll<HTMLButtonElement>("button")];
    expect(buttons.map((button) => button.textContent)).toEqual(["取消", "放弃未保存输入并离开", "保存后离开"]);
    let saving!: Promise<void>;
    await act(async () => { saving = dialog.onOk(); void saving.catch(() => undefined); });
    // 保留已经渲染的页脚节点，模拟写盘开始前捕获的点击，不能用旧的空闲状态放行。
    let left = false; void leaving.then(() => { left = true; });
    await act(async () => buttons[1].click());
    expect(mocks.destroy).not.toHaveBeenCalled(); expect(left).toBe(false);
    await act(async () => { rejectSave(new Error("磁盘写入失败")); await expect(saving).rejects.toThrow("不能离开"); });
    await act(async () => buttons[1].click());
    expect(mocks.destroy).toHaveBeenCalledTimes(1); expect(await leaving).toBe(true);
  });
});
