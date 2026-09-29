// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthUser, OfflineDraftRecord, ReviewTask } from "../types";
import type { OfflineDraftEnvelope } from "../lib/offlineStore";
import { OfflineWorkbench } from "./OfflineWorkbench";

const mocks = vi.hoisted(() => ({
  durable: vi.fn(() => true), getTask: vi.fn(), listTasks: vi.fn(), submit: vi.fn(),
  save: vi.fn(), update: vi.fn(), listDrafts: vi.fn(), loadDraft: vi.fn(), purge: vi.fn(), log: vi.fn(),
  buttons: new Map<string, { onClick: () => void; disabled: boolean }>(),
  changeScore: undefined as undefined | ((score: number) => void)
}));
vi.mock("../api/review", () => ({
  getReviewTask: mocks.getTask, listReviewTasks: mocks.listTasks, submitHumanGrade: mocks.submit,
  listAiGrades: vi.fn(async () => ({ grades: [{ suggested_score: 6, max_score: 10, created_at: "2026-09-26" }] }))
}));
vi.mock("../api/papers", () => ({ listQuestions: vi.fn(async () => ({ questions: [{ id: "question-A", score: 10 }] })) }));
vi.mock("../api/submissions", () => ({
  listAnswerSegments: vi.fn(async () => ({ segments: [] })),
  listSubmissionPages: vi.fn(async () => ({ pages: [] })),
  listOcrTasks: vi.fn(async () => ({ tasks: [] }))
}));
vi.mock("../api/files", () => ({ downloadFileBlob: vi.fn() }));
vi.mock("../lib/durableStore", () => ({ hasDurableDesktopStore: mocks.durable }));
vi.mock("../lib/offlineStore", () => ({
  readOfflineDraftEnvelopes: () => [], listOfflineDraftEnvelopes: mocks.listDrafts,
  saveOfflineDraft: mocks.save, updateOfflineDraftStatus: mocks.update,
  loadOfflineDraft: mocks.loadDraft, purgeExpiredOfflineDrafts: mocks.purge
}));
vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  const ListItem = Object.assign(({ children, actions }: { children?: ReactNode; actions?: ReactNode[] }) => <div>{children}{actions}</div>, { Meta: ({ title, description }: { title: ReactNode; description: ReactNode }) => <div>{title}{description}</div> });
  const List = Object.assign(({ dataSource = [], renderItem }: { dataSource?: unknown[]; renderItem: (value: unknown) => ReactNode }) => <>{dataSource.map((item, index) => <div key={index}>{renderItem(item)}</div>)}</>, { Item: ListItem });
  const Input = Object.assign(({ value = "", onChange }: { value?: string; onChange?: React.ChangeEventHandler<HTMLInputElement> }) => <input value={value} onChange={onChange} />, {
    Password: () => <input type="password" />, TextArea: Box
  });
  return {
    Alert: ({ message }: { message: ReactNode }) => <div role="alert">{message}</div>,
    Button: ({ children, disabled = false, onClick }: { children: ReactNode; disabled?: boolean; onClick: () => void }) => {
      mocks.buttons.set(String(children), { disabled, onClick });
      return <button disabled={disabled} onClick={onClick}>{children}</button>;
    },
    Empty: Box, Form: Object.assign(Box, { Item: Box }), Input,
    InputNumber: ({ value, onChange }: { value: number; onChange: (score: number) => void }) => {
      mocks.changeScore = onChange;
      return <span>{value}</span>;
    },
    List, Space: Box, Table: Box, Tag: Box
  };
});

describe("offline workbench submission persistence", () => {
  let root: Root;
  let container: HTMLDivElement;
  let rows: Map<string, OfflineDraftRecord>;
  let payloads: Map<string, OfflineDraftRecord>;
  const task = { id: "task-A", revision: 3, status: "in_progress", anonymous_code: "anon-A", assigned_to: "teacher", question_id: "question-A", exam_id: "exam-A" } as ReviewTask;

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.buttons.clear();
    mocks.durable.mockReturnValue(true);
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    rows = new Map();
    payloads = new Map();
    mocks.getTask.mockResolvedValue({ task });
    mocks.listTasks.mockResolvedValue({ tasks: [task] });
    mocks.submit.mockResolvedValue({});
    mocks.log.mockResolvedValue(undefined);
    mocks.purge.mockResolvedValue(0);
    mocks.save.mockImplementation(async (record: OfflineDraftRecord) => {
      rows.set(record.taskId, structuredClone(record));
      payloads.set(record.taskId, structuredClone(record));
    });
    mocks.update.mockImplementation(async (taskId: string, patch: Partial<OfflineDraftEnvelope>) => {
      const saved = rows.get(taskId);
      // Match native SQLite's update-only contract: absent rows are errors.
      if (!saved) throw new Error("offline draft was not found");
      Object.assign(saved, patch);
    });
    mocks.listDrafts.mockImplementation(async () => [...rows.values()]);
    // Status writes only change metadata in SQLite and the browser envelope.
    mocks.loadDraft.mockImplementation(async (taskId: string) => payloads.get(taskId));
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });

  async function click(label: string) {
    const action = mocks.buttons.get(label)!;
    expect(action, label).toBeDefined();
    expect(action.disabled, label).toBe(false);
    await act(async () => { action.onClick(); });
  }

  async function mount(isOnline = true) {
    await act(async () => {
      root.render(<OfflineWorkbench durableScopeKey="scope-A" client={{} as never} token="token" user={{ id: "teacher" } as AuthUser} isOnline={isOnline} onLog={mocks.log} />);
    });
    await click("获取我的任务");
    await click("下载任务包");
  }

  it("encrypts the current unsaved draft before conflict checking or submitting", async () => {
    await mount();
    await act(async () => { mocks.changeScore!(8); });
    expect(rows.size).toBe(0);
    mocks.getTask.mockImplementation(async () => {
      expect(rows.get("task-A")).toMatchObject({ syncStatus: "syncing", draft: { score: 8 } });
      return { task };
    });
    mocks.submit.mockImplementation(async () => {
      expect(rows.get("task-A")).toMatchObject({ syncStatus: "syncing", draft: { score: 8 } });
      return {};
    });
    await click("同步提交");
    expect(mocks.save).toHaveBeenCalledWith(expect.objectContaining({ taskId: "task-A", packageSnapshot: expect.objectContaining({ task }), draft: expect.objectContaining({ score: 8 }) }), "", "scope-A");
    expect(mocks.submit).toHaveBeenCalledWith(expect.anything(), "task-A", expect.objectContaining({ score: 8, expected_revision: 3 }));
    expect(rows.get("task-A")?.syncStatus).toBe("synced");
    expect(container.textContent).toContain("同步成功，服务端已接收人工评分");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
  });

  it("stops before all server operations if encrypted persistence fails", async () => {
    await mount();
    mocks.getTask.mockClear();
    mocks.save.mockRejectedValue(new Error("磁盘已满"));
    await click("同步提交");
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.update).not.toHaveBeenCalled();
    expect(container.textContent).toContain("草稿加密保存失败，尚未提交");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(false);
  });

  it("requires an encryption key before offering browser-mode sync", async () => {
    mocks.durable.mockReturnValue(false);
    await mount();
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    // Also guard the handler when an already queued click reaches it.
    await act(async () => { mocks.buttons.get("同步提交")!.onClick(); });
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(container.textContent).toContain("未配置密钥时禁止同步草稿");
  });

  it("keeps the server success and blocks retry when local receipt, listing, and log fail", async () => {
    await mount();
    mocks.update.mockRejectedValue(new Error("本地状态写入失败"));
    mocks.listDrafts.mockRejectedValue(new Error("本地列表读取失败"));
    mocks.log.mockRejectedValue(new Error("本地日志写入失败"));
    await click("同步提交");
    expect(mocks.update).toHaveBeenCalledTimes(1);
    expect(mocks.update).toHaveBeenCalledWith("task-A", expect.objectContaining({ syncStatus: "synced" }), "scope-A");
    expect(container.textContent).toContain("同步成功，服务端已接收人工评分");
    expect(container.textContent).toContain("本地状态保存失败");
    expect(container.textContent).toContain("本地日志保存失败");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
    await act(async () => { mocks.buttons.get("同步提交")!.onClick(); });
    expect(mocks.submit).toHaveBeenCalledTimes(1);
  });

  it("does not let a log failure turn an acknowledged grade into a failed draft", async () => {
    await mount();
    mocks.log.mockRejectedValue(new Error("日志写入失败"));
    await click("同步提交");
    expect(rows.get("task-A")?.syncStatus).toBe("synced");
    expect(mocks.update).toHaveBeenCalledTimes(1);
    expect(container.textContent).toContain("同步成功，服务端已接收人工评分");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
  });

  it("catches secondary local errors after a server rejection", async () => {
    await mount();
    mocks.submit.mockRejectedValue(new Error("服务端拒绝评分"));
    mocks.update.mockRejectedValue(new Error("本地状态写入失败"));
    mocks.log.mockRejectedValue(new Error("本地日志写入失败"));
    await click("同步提交");
    expect(mocks.update).toHaveBeenCalledTimes(1);
    expect(mocks.update).toHaveBeenCalledWith("task-A", expect.objectContaining({ syncStatus: "failed" }), "scope-A");
    expect(container.textContent).toContain("服务端拒绝评分");
    expect(container.textContent).toContain("本地状态保存失败");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(false);
  });

  it("blocks two clicks before the first asynchronous save resolves", async () => {
    await mount();
    let release!: () => void;
    mocks.save.mockImplementationOnce((record: OfflineDraftRecord) => new Promise<void>((resolve) => {
      release = () => { rows.set(record.taskId, record); resolve(); };
    }));
    const clickBeforeRerender = mocks.buttons.get("同步提交")!.onClick;
    await act(async () => { clickBeforeRerender(); clickBeforeRerender(); });
    expect(mocks.save).toHaveBeenCalledTimes(1);
    expect(mocks.submit).not.toHaveBeenCalled();
    await act(async () => { release(); });
    expect(mocks.submit).toHaveBeenCalledTimes(1);
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    // A queued callback captured before React re-rendered is guarded as well.
    await act(async () => { clickBeforeRerender(); });
    expect(mocks.submit).toHaveBeenCalledTimes(1);
  });

  it("does not interleave a manual save or task package load with submission", async () => {
    await mount();
    let release!: () => void;
    mocks.save.mockImplementationOnce((record: OfflineDraftRecord) => new Promise<void>((resolve) => {
      release = () => { rows.set(record.taskId, record); resolve(); };
    }));
    const oldSync = mocks.buttons.get("同步提交")!.onClick;
    const oldDownload = mocks.buttons.get("下载任务包")!.onClick;
    await click("加密保存草稿");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    await act(async () => { oldSync(); oldDownload(); });
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.getTask).toHaveBeenCalledTimes(1);
    await act(async () => { release(); });
    await click("同步提交");
    expect(mocks.submit).toHaveBeenCalledTimes(1);
  });

  it("blocks package replacement and manual saves while a submission is in flight", async () => {
    await mount();
    await click("加密保存草稿");
    let release!: () => void;
    mocks.submit.mockImplementationOnce(() => new Promise<void>((resolve) => { release = resolve; }));
    const oldDownload = mocks.buttons.get("下载任务包")!.onClick;
    const oldSave = mocks.buttons.get("加密保存草稿")!.onClick;
    const oldLoad = mocks.buttons.get("加载")!.onClick;
    await click("同步提交");
    expect(mocks.buttons.get("下载任务包")?.disabled).toBe(true);
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
    expect(mocks.buttons.get("加载")?.disabled).toBe(true);
    await act(async () => { oldDownload(); oldSave(); oldLoad(); });
    expect(mocks.save).toHaveBeenCalledTimes(2);
    expect(mocks.loadDraft).not.toHaveBeenCalled();
    expect(mocks.getTask).toHaveBeenCalledTimes(2);
    await act(async () => { release(); });
    expect(rows.get("task-A")?.syncStatus).toBe("synced");
  });

  it("waits for package replacement before allowing a captured sync or save action", async () => {
    await mount();
    let release!: () => void;
    mocks.getTask.mockImplementationOnce(() => new Promise<{ task: ReviewTask }>((resolve) => { release = () => resolve({ task }); }));
    const oldSync = mocks.buttons.get("同步提交")!.onClick;
    const oldSave = mocks.buttons.get("加密保存草稿")!.onClick;
    await click("下载任务包");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
    await act(async () => { oldSync(); oldSave(); });
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
    await act(async () => { release(); });
    await click("同步提交");
    expect(mocks.submit).toHaveBeenCalledTimes(1);
  });

  it("retains the acknowledged task after downloading it again when its local receipt failed", async () => {
    await mount();
    mocks.update.mockRejectedValue(new Error("本地状态写入失败"));
    await click("同步提交");
    expect(rows.get("task-A")?.syncStatus).toBe("syncing");
    await click("下载任务包");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
    await act(async () => { mocks.buttons.get("同步提交")!.onClick(); });
    expect(mocks.submit).toHaveBeenCalledTimes(1);
  });

  it("loads the latest status metadata instead of the encrypted pre-submit status", async () => {
    await mount();
    await click("同步提交");
    expect(payloads.get("task-A")?.syncStatus).toBe("syncing");
    expect(rows.get("task-A")?.syncStatus).toBe("synced");
    // Reopen the workbench so only durable metadata can supply the receipt.
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    await mount();
    await click("加载");
    expect(container.textContent).toContain("同步成功，服务端已接收人工评分");
    expect(container.textContent).not.toContain("同步中");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
    expect(mocks.submit).toHaveBeenCalledTimes(1);
  });

  it("persists an offline retry without updating a nonexistent draft", async () => {
    await mount(false);
    mocks.getTask.mockClear();
    await click("同步提交");
    expect(rows.get("task-A")).toMatchObject({ syncStatus: "failed", draft: { score: 6 } });
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(container.textContent).toContain("加密草稿可稍后重试");
  });

  it("preserves a detected revision conflict without posting a grade", async () => {
    await mount();
    mocks.getTask.mockResolvedValue({ task: { ...task, revision: 4 } });
    await click("同步提交");
    expect(rows.get("task-A")?.syncStatus).toBe("conflict");
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(container.textContent).toContain("服务端任务版本已变化");
  });

  it("handles an initial local listing failure without rejecting the effect", async () => {
    mocks.listDrafts.mockRejectedValue(new Error("本地存储不可用"));
    await act(async () => {
      root.render(<OfflineWorkbench durableScopeKey="scope-A" client={{} as never} token="token" user={{ id: "teacher" } as AuthUser} isOnline onLog={mocks.log} />);
    });
    expect(container.textContent).toContain("本地列表读取失败");
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.listDrafts).toHaveBeenCalledTimes(1);
  });

  it("handles task and package failures when error logging also fails", async () => {
    await mount();
    mocks.listTasks.mockRejectedValue(new Error("任务读取失败"));
    mocks.getTask.mockRejectedValue(new Error("任务包读取失败"));
    mocks.log.mockRejectedValue(new Error("日志写入失败"));
    await click("获取我的任务");
    expect(container.textContent).toContain("本地日志保存失败");
    await click("下载任务包");
    expect(container.textContent).toContain("任务包读取失败");
    expect(mocks.submit).not.toHaveBeenCalled();
  });

  it("reports cache cleanup failures without rejecting the click handler", async () => {
    await mount();
    mocks.purge.mockRejectedValueOnce(new Error("磁盘不可用"));
    await click("清理过期缓存");
    expect(container.textContent).toContain("本地缓存清理失败");
    mocks.purge.mockResolvedValueOnce(2);
    mocks.log.mockRejectedValue(new Error("日志写入失败"));
    await click("清理过期缓存");
    expect(container.textContent).toContain("已清理 2 条过期本地缓存，但本地列表或日志更新失败");
  });
});
