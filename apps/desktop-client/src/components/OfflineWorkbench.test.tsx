// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthUser, OfflineDraftRecord, ReviewTask } from "../types";
import { DesktopApiClient } from "../api/client";
import type { OfflineDraftEnvelope } from "../lib/offlineStore";
import { OfflineWorkbench, type OfflineLeaveGuard } from "./OfflineWorkbench";

const mocks = vi.hoisted(() => ({
  durable: vi.fn(() => true), getTask: vi.fn(), getContext: vi.fn(), listTasks: vi.fn(), submit: vi.fn(), getCommand: vi.fn(),
  save: vi.fn(), update: vi.fn(), listDrafts: vi.fn(), loadDraft: vi.fn(), purge: vi.fn(), log: vi.fn(),
  listQuestions: vi.fn(), listSegments: vi.fn(), listPages: vi.fn(), listAiGrades: vi.fn(), downloadImage: vi.fn(), createURL: vi.fn(), revokeURL: vi.fn(),
  confirm: vi.fn(),
  buttons: new Map<string, { onClick: () => void; disabled: boolean }>(),
  changeScore: undefined as undefined | ((score: number) => void)
}));
vi.mock("../api/review", () => ({
  getReviewTask: mocks.getTask, listReviewTasks: mocks.listTasks, submitHumanGrade: mocks.submit, getReviewCommand: mocks.getCommand,
  getReviewTaskContext: mocks.getContext,
  downloadReviewTaskImage: mocks.downloadImage,
  listAiGrades: mocks.listAiGrades
}));
vi.mock("../api/papers", () => ({ listQuestions: mocks.listQuestions }));
vi.mock("../api/exams", () => ({ listExams: vi.fn(async () => ({ exams: [{ id: "exam-A", name: "期中考试" }] })) }));
vi.mock("../api/submissions", () => ({
  listAnswerSegments: mocks.listSegments,
  listSubmissionPages: mocks.listPages,
  listOcrTasks: vi.fn(async () => ({ tasks: [] }))
}));
vi.mock("../api/files", () => ({ downloadFileBlob: mocks.downloadImage }));
vi.mock("../lib/durableStore", () => ({ hasDurableDesktopStore: mocks.durable }));
vi.mock("../lib/offlineStore", () => ({
  readOfflineDraftEnvelopes: () => [], listOfflineDraftEnvelopes: mocks.listDrafts,
  saveOfflineDraft: mocks.save, updateOfflineDraftStatus: mocks.update,
  loadOfflineDraft: mocks.loadDraft, purgeExpiredOfflineDrafts: mocks.purge
}));
vi.mock("antd", () => {
  const Box = ({ children }: { children?: ReactNode }) => <div>{children}</div>;
  const ListItem = Object.assign(({ children, actions }: { children?: ReactNode; actions?: ReactNode[] }) => <div>{children}{actions}</div>, { Meta: ({ title, description }: { title: ReactNode; description: ReactNode }) => <div>{title}<div data-draft-description>{description}</div></div> });
  const List = Object.assign(({ dataSource = [], renderItem }: { dataSource?: unknown[]; renderItem: (value: unknown) => ReactNode }) => <>{dataSource.map((item, index) => <div key={index}>{renderItem(item)}</div>)}</>, { Item: ListItem });
  const Input = Object.assign(({ value = "", onChange }: { value?: string; onChange?: React.ChangeEventHandler<HTMLInputElement> }) => <input value={value} onChange={onChange} />, {
    Password: () => <input type="password" />, TextArea: Box
  });
  return {
    Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <div role="alert">{message}{description}</div>,
    Button: ({ children, disabled = false, onClick }: { children: ReactNode; disabled?: boolean; onClick: () => void }) => {
      mocks.buttons.set(String(children), { disabled, onClick });
      if (String(children).startsWith("下载任务包：")) mocks.buttons.set("下载任务包", { disabled, onClick });
      return <button disabled={disabled} onClick={onClick}>{children}</button>;
    },
    Empty: Box, Form: Object.assign(Box, { Item: Box }), Input,
    InputNumber: ({ value, onChange }: { value: number; onChange: (score: number) => void }) => {
      mocks.changeScore = onChange;
      return <span>{value}</span>;
    },
    List, Modal: { confirm: mocks.confirm }, Space: Box,
    Table: ({ dataSource = [], columns, rowClassName }: { dataSource?: ReviewTask[]; columns: { render?: (value: unknown, task: ReviewTask) => ReactNode }[]; rowClassName?: (task: ReviewTask) => string }) => <>{dataSource.map((task) => <div key={task.id} className={rowClassName?.(task)}>{columns[columns.length - 1]?.render?.(undefined, task)}</div>)}</>,
    Tag: ({ children, color }: { children?: ReactNode; color?: string }) => <span data-tag-color={color}>{children}</span>
  };
});

describe("offline workbench submission persistence", () => {
  let root: Root;
  let container: HTMLDivElement;
  let rows: Map<string, OfflineDraftRecord>;
  let payloads: Map<string, OfflineDraftRecord>;
  let leaveGuard: OfflineLeaveGuard | null;
  const registerLeaveGuard = vi.fn((guard: OfflineLeaveGuard | null) => { leaveGuard = guard; });
  const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu", getToken: () => "token" });
  const user = { id: "teacher", tenant_id: "tenant-A" } as AuthUser;
  const task = { id: "task-A", revision: 3, status: "in_progress", anonymous_code: "anon-A", assigned_to: "teacher", question_id: "question-A", exam_id: "exam-A", answer_segment_id: "segment-A", submission_id: "submission-A" } as ReviewTask;
  const taskContext = {
    task, question: { id: "question-A", score: 10 },
    frozen_rubric: { id: "rubric-A", question_id: "question-A", version: "frozen-v1", status: "active", max_score: 10, points: [] },
    answer_artifact: { answer_segment_id: "segment-A", raw_answer: "学生作答", ocr_text: "识别作答", status: "ready", segment_image_url: "/api/v1/review-tasks/task-A/segment-image" },
    ai_second_opinion: { available: true, presentation: "explicit_second_opinion", score_prefill_allowed: true, metadata: { suggested_score: 6, max_score: 10, confidence: 0.8, created_at: "2026-09-26" } }
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mocks.buttons.clear();
    leaveGuard = null;
    mocks.confirm.mockImplementation((options: { onOk: () => void }) => { options.onOk(); });
    mocks.durable.mockReturnValue(true);
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    rows = new Map();
    payloads = new Map();
    mocks.getTask.mockResolvedValue({ task });
    mocks.getContext.mockResolvedValue({ context: structuredClone(taskContext) });
    mocks.listQuestions.mockResolvedValue({ questions: [{ id: "question-A", stem: "当前可编辑题干", rubric: { version: "editable-v9", points: [{ id: "wrong", description: "当前细则", score: 10 }] } }] });
    mocks.listSegments.mockResolvedValue({ segments: [] });
    mocks.listPages.mockResolvedValue({ pages: [] });
    mocks.downloadImage.mockResolvedValue({ blob: new Blob(["answer"], { type: "image/png" }), contentType: "image/png", filename: "answer.png" });
    mocks.createURL.mockReturnValue("blob:answer-A");
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: mocks.createURL });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: mocks.revokeURL });
    mocks.listTasks.mockResolvedValue({ tasks: [task] });
    mocks.submit.mockResolvedValue({});
    mocks.getCommand.mockReset().mockImplementation(async (_client: unknown, commandId: string) => ({ command_id: commandId, status: "not_accepted" }));
    mocks.log.mockResolvedValue(undefined);
    mocks.purge.mockResolvedValue(0);
    mocks.save.mockImplementation(async (record: OfflineDraftRecord) => {
      rows.set(record.taskId, structuredClone(record));
      payloads.set(record.taskId, structuredClone(record));
    });
    mocks.update.mockImplementation(async (taskId: string, patch: Partial<OfflineDraftEnvelope>) => {
      const saved = rows.get(taskId);
      // 与原生 SQLite 只更新已有记录的契约一致：记录不存在时应报错。
      if (!saved) throw new Error("offline draft was not found");
      Object.assign(saved, patch);
    });
    mocks.listDrafts.mockImplementation(async () => [...rows.values()]);
    // 状态写入只改变 SQLite 和浏览器封装中的元数据。
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

  async function displayImage() {
    const image = container.querySelector("img");
    if (!image) return;
    Object.defineProperties(image, { naturalWidth: { configurable: true, value: 400 }, naturalHeight: { configurable: true, value: 200 } });
    await act(async () => image.dispatchEvent(new Event("load")));
  }

  async function mount(isOnline = true, displayDownloadedImage = true) {
    await act(async () => {
      root.render(<OfflineWorkbench key="scope-A" durableScopeKey="scope-A" client={client} token="token" user={user} isOnline={isOnline} onLog={mocks.log} registerLeaveGuard={registerLeaveGuard} />);
    });
    await click("获取我的任务");
    await click("下载任务包");
    if (displayDownloadedImage) await displayImage();
    // 提交回执测试需教师主动填写最终分；AI 建议不再默认填入人工评分。
    await act(async () => mocks.changeScore!(6));
  }

  it("identifies the selected answer and offers the next task only after confirmed submission", async () => {
    const next = { ...task, id: "task-B", anonymous_code: "anon-B", question_no: "2" };
    mocks.listTasks.mockResolvedValue({ tasks: [task, next] });
    await mount();
    expect(container.querySelector(".selected-review-task")).not.toBeNull();
    expect(container.textContent).toContain("当前选择：anon-A");
    expect(container.textContent).toContain("期中考试");
    expect(container.textContent).toContain("下载任务包：anon-A");
    expect(container.textContent).not.toContain("下一份：anon-B");
    await click("同步提交");
    expect(container.textContent).toContain("下一份：anon-B");
    mocks.getTask.mockResolvedValue({ task: next });
    mocks.getContext.mockResolvedValue({ context: { task: next, question: { id: "question-A", score: 10 }, frozen_rubric: { version: "frozen-v1", max_score: 10, points: [] } } });
    await click("下一份：anon-B");
    expect(mocks.getTask).toHaveBeenLastCalledWith(client, "task-B");
    expect(container.textContent).toContain("当前选择：anon-B");
  });

  it("preserves local draft descriptions and sync status colors", async () => {
    const statuses = [
      ["draft", "本地草稿", "default"], ["synced", "已同步", "success"],
      ["conflict", "存在冲突", "warning"], ["failed", "同步失败", "error"],
      ["syncing", "同步中", "processing"]
    ] as const;
    mocks.listDrafts.mockResolvedValue(statuses.map(([syncStatus]) => ({
      taskId: syncStatus, anonymousCode: `anon-${syncStatus}`, syncStatus,
      savedAt: "2026-09-26T10:30:00", expiresAt: "2026-10-03T10:30:00",
      syncMessage: syncStatus === "draft" ? undefined : "已记录处理结果"
    })));
    await act(async () => {
      root.render(<OfflineWorkbench key="scope-A" durableScopeKey="scope-A" client={client} token="token" user={user} isOnline onLog={mocks.log} />);
    });
    const descriptions = [...container.querySelectorAll("[data-draft-description]")].map((item) => item.textContent);
    expect(descriptions[0]).toBe("2026/9/26 10:30:00 / 过期 2026/10/3 10:30:00");
    expect(descriptions.slice(1)).toEqual(Array(4).fill(`${descriptions[0]} / 已记录处理结果`));
    for (const [, label, color] of statuses) {
      const tag = [...container.querySelectorAll("[data-tag-color]")].find((item) => item.textContent === label);
      expect(tag?.getAttribute("data-tag-color")).toBe(color);
    }
  });

  it("keeps final grading empty until the teacher explicitly adopts the AI score", async () => {
    await act(async () => root.render(<OfflineWorkbench durableScopeKey="scope-A" client={client} token="token" user={user} isOnline onLog={mocks.log} />));
    await click("获取我的任务"); await click("下载任务包"); await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft).toMatchObject({ score: null, studentFeedback: "", reason: "" });
    await click("同步提交"); expect(mocks.submit).not.toHaveBeenCalled();
    await click("采用 AI 建议分"); await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft.score).toBe(6);
  });

  it("reads answer text and authorized suggestions only from the assigned task context", async () => {
    await mount();
    expect(container.textContent).toContain("学生作答");
    expect(container.textContent).toContain("建议分 6/10");
    expect(mocks.downloadImage).toHaveBeenCalledWith(client, "/api/v1/review-tasks/task-A/segment-image", "task-A");
    expect(mocks.listSegments).not.toHaveBeenCalled(); expect(mocks.listPages).not.toHaveBeenCalled();
    expect(mocks.listQuestions).not.toHaveBeenCalled(); expect(mocks.listAiGrades).not.toHaveBeenCalled();
  });

  it("keeps current and historical answer candidates distinct from score suggestions", async () => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, ai_candidates: [
      { id: "current", answer_segment_id: "segment-A", source: "ocr", is_current: true, display_text: "当前识别候选", confidence: 0.9, payload: { suggested_score: 99 } },
      { id: "history", answer_segment_id: "segment-A", source: "vlm", is_current: false, display_text: "历史图像候选" },
      { id: "other", answer_segment_id: "segment-B", source: "ocr", is_current: true, display_text: "其他答卷候选" }
    ] } });
    await mount(); await click("加密保存草稿");
    expect(container.textContent).toContain("当前候选来源：文字识别");
    expect(container.textContent).toContain("历史候选来源：图像识别");
    expect(container.textContent).toContain("当前识别候选"); expect(container.textContent).toContain("历史图像候选");
    expect(container.textContent).not.toContain("其他答卷候选"); expect(container.textContent).not.toContain("建议分 99");
    expect(payloads.get("task-A")?.packageSnapshot.aiGrades[0]?.suggested_score).toBe(6);
  });

  it("does not recover a score from candidate payloads when human-primary context forbids prefill", async () => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext,
      question_snapshot: { risk_tier: "R3", scoring_policy_snapshot: { mode: "HUMAN_PRIMARY" } },
      ai_second_opinion: { ...taskContext.ai_second_opinion, score_prefill_allowed: false,
        metadata: { ...taskContext.ai_second_opinion.metadata, teacher_note: "请独立核对计算步骤" } },
      ai_candidates: [{ id: "candidate", answer_segment_id: "segment-A", source: "ocr", is_current: true, display_text: "识别证据", payload: { suggested_score: 6 } }]
    } });
    await mount();
    expect(mocks.buttons.has("采用 AI 建议分")).toBe(false); expect(container.textContent).not.toContain("建议分 6/10");
    expect(container.textContent).toContain("识别证据"); expect(mocks.listAiGrades).not.toHaveBeenCalled();
    expect(container.textContent).not.toContain("请独立核对计算步骤");
    await click("查看 AI 辅助意见");
    expect(container.textContent).toContain("请独立核对计算步骤"); expect(container.textContent).not.toContain("建议分 6/10");
  });

  it("blocks a new submission when an image URL exists but its download failed", async () => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, answer_artifact: { ...taskContext.answer_artifact, raw_answer: " ", ocr_text: "" } } });
    mocks.downloadImage.mockRejectedValue(new Error("图片下载失败"));
    await mount();
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    expect(container.textContent).toContain("没有可核对答卷材料，不能提交");
    // 绕过按钮禁用模拟已捕获回调；处理函数仍必须阻止保存命令和网络提交。
    await act(async () => mocks.buttons.get("同步提交")!.onClick());
    expect(mocks.save).not.toHaveBeenCalled(); expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.getCommand).not.toHaveBeenCalled();
  });

  it("人工主导辅助意见只展示可读字段，重新载入或换任务后恢复收起", async () => {
    const nextTask = { ...task, id: "task-B", anonymous_code: "anon-B", answer_segment_id: "segment-B" };
    const opinion = { ...taskContext.ai_second_opinion, score_prefill_allowed: false,
      metadata: { teacher_note: "先核对教师说明", student_feedback: "再核对学生反馈", confidence: 0.87, suggested_score: 9, max_score: 10 } };
    mocks.listTasks.mockResolvedValue({ tasks: [task, nextTask] });
    const context = { ...taskContext, question_snapshot: { risk_tier: "R3", scoring_policy_snapshot: { mode: "HUMAN_PRIMARY" } }, ai_second_opinion: opinion };
    mocks.getContext.mockResolvedValue({ context });
    await mount();
    expect(container.textContent).not.toContain("先核对教师说明");
    await click("查看 AI 辅助意见");
    const expectedDetails = ["先核对教师说明", "再核对学生反馈", "辅助意见置信度 87%"];
    expect([...container.querySelectorAll(".package-view p")].map((node) => node.textContent).filter((text) => expectedDetails.includes(text!))).toEqual(expectedDetails);
    expect(container.textContent).not.toContain("建议分 9/10");
    expect(mocks.buttons.has("采用 AI 建议分")).toBe(false);
    await click("加密保存草稿");
    const stored = payloads.get("task-A")!;
    const storedPackage = stored.packageSnapshot as typeof stored.packageSnapshot & { aiSecondOpinion: { metadata: Record<string, unknown> } };
    // 同任务重新载入不同下载代次的保存包，也必须收起此前展开的辅助意见。
    storedPackage.downloadedAt = new Date(Date.parse(storedPackage.downloadedAt) + 1).toISOString();
    storedPackage.aiSecondOpinion.metadata = { teacher_note: 7, student_feedback: ["不可显示反馈"], confidence: "0.91", suggested_score: 9 };
    await click("加载");
    expect(container.textContent).toContain("查看 AI 辅助意见");
    expect(container.textContent).not.toContain("收起 AI 辅助意见");
    expect(container.textContent).not.toContain("辅助意见置信度");
    await click("查看 AI 辅助意见");
    expect(container.textContent).not.toContain("不可显示反馈");
    expect(container.textContent).not.toContain("辅助意见置信度");
    expect(container.textContent).not.toContain("建议分 9/10");
    mocks.getTask.mockResolvedValue({ task: nextTask });
    mocks.getContext.mockResolvedValue({ context: { ...context, task: nextTask,
      answer_artifact: { ...taskContext.answer_artifact, answer_segment_id: "segment-B", segment_image_url: "/api/v1/review-tasks/task-B/segment-image" } } });
    await click("选择"); await click("下载任务包");
    expect(container.textContent).toContain("查看 AI 辅助意见");
    expect(container.textContent).not.toContain("收起 AI 辅助意见");
    expect(container.textContent).not.toContain("先核对教师说明");
    expect(container.textContent).not.toContain("再核对学生反馈");
    expect(container.textContent).not.toContain("辅助意见置信度");
  });

  it("blocks empty material and permits an OCR-only answer without downloading another resource", async () => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, answer_artifact: { ...taskContext.answer_artifact, raw_answer: "", ocr_text: " ", segment_image_url: "" } } });
    await mount();
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true); expect(mocks.downloadImage).not.toHaveBeenCalled();
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, answer_artifact: { ...taskContext.answer_artifact, raw_answer: "", ocr_text: "可读识别文字", segment_image_url: "" } } });
    await click("下载任务包");
    expect(container.textContent).toContain("可读识别文字"); expect(mocks.buttons.get("同步提交")?.disabled).toBe(false);
    await click("同步提交"); expect(mocks.submit).toHaveBeenCalledOnce();
  });

  it("requires restoring an image-only encrypted package before creating a new command", async () => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, answer_artifact: { ...taskContext.answer_artifact, raw_answer: "", ocr_text: "" } } });
    await mount(); await click("加密保存草稿"); await click("加载");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    await click("恢复答卷图片"); expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    await displayImage(); expect(mocks.buttons.get("同步提交")?.disabled).toBe(false);
    await click("同步提交"); expect(mocks.submit).toHaveBeenCalledOnce();
  });

  it("keeps a downloaded corrupt image blocked and revokes readability after a display error", async () => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, answer_artifact: { ...taskContext.answer_artifact, raw_answer: "", ocr_text: "" } } });
    await mount(true, false);
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    await displayImage(); expect(mocks.buttons.get("同步提交")?.disabled).toBe(false);
    const capturedSubmit = mocks.buttons.get("同步提交")!.onClick;
    await act(async () => container.querySelector("img")!.dispatchEvent(new Event("error")));
    expect(container.textContent).toContain("答卷预览未能显示"); expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    // 已捕获的按钮回调也需读取现在的显示结果，不能沿用图片报错前的可读状态。
    await act(async () => capturedSubmit());
    expect(mocks.submit).not.toHaveBeenCalled(); expect(mocks.save).not.toHaveBeenCalled();
  });

  it.each([
    ["image/tiff", "answer.tiff"], ["application/octet-stream", "answer.bin"], ["application/pdf", "answer.pdf"]
  ])("does not treat downloaded %s as a decoded answer image", async (contentType, filename) => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, answer_artifact: { ...taskContext.answer_artifact, raw_answer: "", ocr_text: "" } } });
    mocks.downloadImage.mockResolvedValue({ blob: new Blob(["document"], { type: contentType }), contentType, filename });
    await mount();
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    const frame = container.querySelector("iframe");
    if (frame) {
      await act(async () => frame.dispatchEvent(new Event("load")));
      expect(container.textContent).toContain("PDF 阅读器载入不能确认内容已显示");
      expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    }
    await act(async () => mocks.buttons.get("同步提交")!.onClick());
    expect(mocks.submit).not.toHaveBeenCalled(); expect(mocks.save).not.toHaveBeenCalled();
  });

  it.each(["succeeded", "processing", "not_accepted"])("checks an image-only pending receipt %s after its session image is lost", async (receiptStatus) => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, answer_artifact: { ...taskContext.answer_artifact, raw_answer: "", ocr_text: "" } } });
    await mount(); mocks.submit.mockRejectedValueOnce(new Error("响应丢失")); await click("同步提交");
    const command = structuredClone(payloads.get("task-A")!.submissionCommand!);
    await click("加载");
    mocks.getCommand.mockResolvedValue({ command_id: command.id, status: receiptStatus, result: { task: { id: "task-A" } } });
    mocks.getTask.mockClear(); mocks.getContext.mockClear(); await click("同步提交");
    expect(mocks.getCommand).toHaveBeenCalledWith(client, command.id); expect(mocks.submit).toHaveBeenCalledTimes(1);
    expect(mocks.getTask).not.toHaveBeenCalled(); expect(mocks.getContext).not.toHaveBeenCalled();
    expect(payloads.get("task-A")!.submissionCommand).toEqual(command);
    if (receiptStatus === "succeeded") expect(container.textContent).toContain("已从原操作回执确认");
    if (receiptStatus === "not_accepted") expect(container.textContent).toContain("当前没有可核对的答卷材料");
  });

  it("restores an old package image through matching task context while preserving old OCR and manual inputs", async () => {
    await mount(); await act(async () => mocks.changeScore!(9)); await click("加密保存草稿");
    const record = payloads.get("task-A")!;
    delete (record.packageSnapshot as { answerText?: string }).answerText;
    delete (record.packageSnapshot as { answerImageUrl?: string }).answerImageUrl;
    delete record.packageSnapshot.assessmentSource; record.packageSnapshot.ocrText = "旧加密包识别文字";
    record.draft.studentFeedback = "保留人工反馈";
    await click("加载"); mocks.getContext.mockClear(); mocks.getTask.mockClear();
    expect(container.textContent).toContain("旧加密包识别文字");
    await click("恢复答卷图片"); await click("加密保存草稿");
    expect(mocks.getContext).toHaveBeenCalledWith(client, "task-A"); expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.listSegments).not.toHaveBeenCalled(); expect(mocks.listPages).not.toHaveBeenCalled();
    expect(payloads.get("task-A")?.draft).toMatchObject({ score: 9, studentFeedback: "保留人工反馈" });
    expect(payloads.get("task-A")?.packageSnapshot.assessmentSource).toBeUndefined();
    expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:answer-A");
  });

  it("rejects mismatched answer artifacts before displaying or downloading them", async () => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, answer_artifact: { ...taskContext.answer_artifact, answer_segment_id: "segment-B" } } });
    await mount();
    expect(container.textContent).toContain("任务冻结上下文与所选任务不一致");
    expect(mocks.downloadImage).not.toHaveBeenCalled(); expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
  });

  it("opens a selected task directly using its saved scoring record", async () => {
    await mount(); await act(async () => mocks.changeScore!(9)); await click("加密保存草稿");
    await act(async () => root.unmount()); root = createRoot(container); mocks.getTask.mockClear();
    await act(async () => root.render(<OfflineWorkbench durableScopeKey="scope-A" client={client} token="token" user={user} isOnline onLog={mocks.log} initialTaskId="task-A" />));
    await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft.score).toBe(9); expect(mocks.getTask).not.toHaveBeenCalled();
  });

  it("preserves an expired unsynced score without calling the destructive cleanup", async () => {
    mocks.listDrafts.mockResolvedValue([{ taskId: "old-unsynced", anonymousCode: "唯一草稿", savedAt: "2026-01-01", expiresAt: "2026-02-01", syncStatus: "draft" }]);
    await mount(); await click("清理过期已同步草稿");
    expect(mocks.purge).not.toHaveBeenCalled(); expect(container.textContent).toContain("未同步草稿继续保留");
  });

  it("saves the task-bound frozen question and rubric without reading the editable question bank", async () => {
    mocks.getContext.mockResolvedValue({ context: { task, question: { id: "question-A", score: 10, stem: "冻结题干" }, frozen_rubric: { id: "rubric-A", question_id: "question-A", version: "frozen-v2", status: "active", max_score: 10, points: [{ id: "frozen-point", description: "冻结评分点", score: 2, required: true }] } } });
    await mount();
    expect(mocks.getContext).toHaveBeenCalledWith(client, "task-A");
    expect(mocks.listQuestions).not.toHaveBeenCalled();
    expect(container.textContent).toContain("冻结题干");
    expect(container.textContent).toContain("冻结评分点");
    await click("加密保存草稿");
    expect(payloads.get("task-A")?.packageSnapshot).toMatchObject({ assessmentSource: "task_context", rubricVersion: "frozen-v2", question: { tenant_id: "tenant-A", stem: "冻结题干", rubric: { version: "frozen-v2", points: [{ id: "frozen-point", score: 2 }] } } });
  });

  it("keeps missing frozen rubric explicit instead of inventing a current rubric", async () => {
    mocks.getContext.mockResolvedValue({ context: { task, question: { id: "question-A", score: 10 }, frozen_rubric: { id: "", question_id: "question-A", version: "", status: "missing", max_score: 10, points: [] } } });
    await mount();
    expect(container.textContent).toContain("本任务缺少冻结评分细则");
    expect(container.textContent).toContain("任务包仅记录缺失状态");
    expect(mocks.listQuestions).not.toHaveBeenCalled();
    await click("加密保存草稿");
    expect(payloads.get("task-A")?.packageSnapshot.question?.rubric).toMatchObject({ status: "missing", version: "", points: [] });
  });

  it("does not claim persistence until saving succeeds and marks subsequent input unsaved", async () => {
    await mount();
    expect(container.textContent).toContain("当前任务材料尚未保存");
    expect(container.textContent).toContain("当前评分输入尚未保存");
    let finishSave!: () => void;
    mocks.save.mockImplementationOnce(() => new Promise<void>((resolve) => { finishSave = resolve; }));
    await click("加密保存草稿");
    expect(container.textContent).toContain("当前任务材料尚未保存");
    await act(async () => { finishSave(); });
    expect(container.textContent).toContain("当前任务材料已加密保存");
    expect(container.textContent).toContain("当前评分草稿已保存");
    await act(async () => { mocks.changeScore!(9); });
    expect(container.textContent).toContain("当前任务材料已加密保存");
    expect(container.textContent).toContain("当前评分输入尚未保存");
  });

  it("keeps navigation pending when a real local save fails and allows cancel without losing input", async () => {
    await mount(); await act(async () => mocks.changeScore!(9));
    let options!: { onOk: () => Promise<void>; onCancel: () => void };
    const dialog = { update: vi.fn(), destroy: vi.fn() };
    mocks.confirm.mockImplementation((value) => { options = value; return dialog; });
    mocks.save.mockRejectedValueOnce(new Error("磁盘写入失败"));
    let leaving!: Promise<boolean>;
    await act(async () => { leaving = leaveGuard!.requestLeave(); });
    await act(async () => { await expect(options.onOk()).rejects.toThrow("不能离开"); });
    expect(payloads.size).toBe(0); expect(container.textContent).toContain("草稿保存失败");
    expect(dialog.update).toHaveBeenCalledWith(expect.objectContaining({ content: expect.stringContaining("未成功") }));
    await act(async () => options.onCancel());
    expect(await leaving).toBe(false); expect(leaveGuard!.shouldBlockClose()).toBe(true);
  });

  it("allows leaving only after the encrypted save finishes", async () => {
    await mount(); await act(async () => mocks.changeScore!(9));
    let options!: { onOk: () => Promise<void> };
    mocks.confirm.mockImplementation((value) => { options = value; return { update: vi.fn(), destroy: vi.fn() }; });
    let leaving!: Promise<boolean>;
    await act(async () => { leaving = leaveGuard!.requestLeave(); });
    await act(async () => { await options.onOk(); });
    expect(await leaving).toBe(true); expect(payloads.get("task-A")?.draft.score).toBe(9);
    expect(leaveGuard!.shouldBlockClose()).toBe(false);
  });

  it("blocks leaving during an in-flight submission without offering discard", async () => {
    await mount(); let finish!: () => void;
    mocks.submit.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve; }));
    await click("同步提交"); mocks.confirm.mockClear();
    await act(async () => { expect(await leaveGuard!.requestLeave()).toBe(false); });
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(leaveGuard!.shouldBlockClose()).toBe(true);
    await act(async () => finish());
  });

  it("reopens and edits a persisted draft after offline local unlock without sending a network request", async () => {
    await mount(); await click("加密保存草稿");
    mocks.getTask.mockClear(); mocks.getContext.mockClear(); mocks.listTasks.mockClear();
    await act(async () => root.render(<OfflineWorkbench key="offline-lease" durableScopeKey="offline-lease" client={client} token={null} user={{ ...user, permissions: [], roles: [] }} isOnline={false} onLog={mocks.log} />));
    await click("加载"); await act(async () => mocks.changeScore!(9)); await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft.score).toBe(9);
    await click("同步提交");
    expect(mocks.submit).not.toHaveBeenCalled(); expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.getContext).not.toHaveBeenCalled(); expect(mocks.listTasks).not.toHaveBeenCalled();
    expect(container.textContent).toContain("当前未登录或离线");
  });

  it("keeps an encrypted manual save and releases the lock when listing fails", async () => {
    await mount();
    mocks.listDrafts.mockRejectedValueOnce(new Error("本地列表读取失败"));
    mocks.log.mockClear();
    await click("加密保存草稿");
    expect(payloads.get("task-A")?.syncStatus).toBe("draft");
    expect(container.textContent).toContain("草稿已加密保存，但本地列表或日志更新失败");
    expect(mocks.log).not.toHaveBeenCalled();
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(false);
  });

  it("clears and revokes the session image when loading a saved draft", async () => {
    mocks.listSegments.mockResolvedValue({ segments: [{ id: "segment-A", submission_page_id: "page-A" }] });
    mocks.listPages.mockResolvedValue({ pages: [{ id: "page-A", file_asset_id: "file-A" }] });
    await mount();
    expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:answer-A");
    expect(container.textContent).toContain("仅在本次会话可用");
    await click("加密保存草稿");
    expect(payloads.get("task-A")).not.toHaveProperty("imagePreview");
    await click("加载");
    expect(container.querySelector("img")).toBeNull();
    expect(mocks.revokeURL).toHaveBeenCalledWith("blob:answer-A");
    expect(container.textContent).toContain("答卷图片尚未加载，不随加密草稿恢复");
  });

  it("restores the saved task image after reopening without replacing manual scores or feedback", async () => {
    mocks.listSegments.mockResolvedValue({ segments: [{ id: "segment-A", submission_page_id: "page-A" }] });
    mocks.listPages.mockResolvedValue({ pages: [{ id: "page-A", file_asset_id: "file-A" }] });
    await mount();
    await act(async () => { mocks.changeScore!(9); });
    await click("加密保存草稿");
    payloads.get("task-A")!.draft.studentFeedback = "人工核对：保留此反馈";
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    await mount(); await click("加载");
    mocks.getTask.mockClear(); mocks.getContext.mockClear(); mocks.save.mockClear();
    await click("恢复答卷图片");
    expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:answer-A");
    expect(mocks.getTask).not.toHaveBeenCalled(); expect(mocks.getContext).not.toHaveBeenCalled();
    expect(mocks.save).not.toHaveBeenCalled();
    await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft).toMatchObject({ score: 9, studentFeedback: "人工核对：保留此反馈" });
    expect(payloads.get("task-A")?.packageSnapshot.task.revision).toBe(3);
  });

  it("keeps unsaved manual input when downloading the same unchanged frozen package again", async () => {
    await mount(); await act(async () => { mocks.changeScore!(9); });
    await click("下载任务包"); await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft.score).toBe(9);
    expect(mocks.confirm).not.toHaveBeenCalled();
  });

  it("retains the old package and scoring input when a changed frozen version is not accepted", async () => {
    await mount(); await act(async () => { mocks.changeScore!(9); });
    await click("加密保存草稿");
    mocks.getContext.mockResolvedValue({ context: { task: { ...task, revision: 4 }, question: { id: "question-A", score: 10 }, frozen_rubric: { id: "rubric-new", question_id: "question-A", version: "frozen-v2", status: "active", max_score: 10, points: [{ id: "new-point", description: "新评分点", score: 10 }] } } });
    mocks.confirm.mockImplementationOnce((options: { onCancel: () => void }) => { options.onCancel(); });
    await click("下载任务包"); await click("加密保存草稿");
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ content: expect.stringContaining("任务版本或冻结评分依据已变化") }));
    expect(payloads.get("task-A")?.draft.score).toBe(9);
    expect(payloads.get("task-A")?.packageSnapshot).toMatchObject({ rubricVersion: "frozen-v1", task: { revision: 3 } });
  });

  it("preserves the total and feedback but requires new point selections after explicit material review", async () => {
    await mount(); await act(async () => { mocks.changeScore!(9); });
    await click("加密保存草稿");
    payloads.get("task-A")!.draft.studentFeedback = "教师的原反馈";
    await click("加载");
    mocks.getContext.mockResolvedValue({ context: { task: { ...task, revision: 4 }, question: { id: "question-A", score: 10 }, frozen_rubric: { id: "rubric-new", question_id: "question-A", version: "frozen-v2", status: "active", max_score: 10, points: [{ id: "new-point", description: "新评分点", score: 10 }] } } });
    await click("下载任务包");
    expect(container.textContent).toContain("评分点已按新材料清空");
    await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft).toMatchObject({ score: 9, studentFeedback: "教师的原反馈", rubricSelections: { "new-point": 0 } });
    expect(payloads.get("task-A")?.packageSnapshot.task.revision).toBe(4);
  });

  it("retains unsaved input when switching tasks or loading a stored draft is cancelled", async () => {
    const otherTask = { ...task, id: "task-B", answer_segment_id: "segment-B" };
    mocks.listTasks.mockResolvedValue({ tasks: [task, otherTask] });
    await mount(); await click("加密保存草稿");
    await act(async () => { mocks.changeScore!(9); });
    mocks.confirm.mockImplementation((options: { onCancel: () => void }) => { options.onCancel(); });
    await click("选择"); mocks.getTask.mockClear();
    await click("下载任务包");
    expect(mocks.getTask).not.toHaveBeenCalled();
    await click("加载"); await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft.score).toBe(9);
    expect(payloads.has("task-B")).toBe(false);
  });

  it.each(["下载任务包", "加载"])("%s 等待未保存确认时，会话变化后保留当前输入", async (action) => {
    const otherTask = { ...task, id: "task-B", answer_segment_id: "segment-B" };
    mocks.listTasks.mockResolvedValue({ tasks: [task, otherTask] });
    await mount(); await click("加密保存草稿");
    await act(async () => { mocks.changeScore!(9); });
    await click("选择");
    let accept!: () => void;
    mocks.confirm.mockImplementationOnce((options: { onOk: () => void }) => { accept = options.onOk; });
    mocks.getTask.mockClear(); mocks.getContext.mockClear(); mocks.loadDraft.mockClear(); mocks.listDrafts.mockClear(); mocks.save.mockClear(); mocks.log.mockClear();
    await click(action);
    expect(mocks.confirm).toHaveBeenCalledOnce();
    // 同一挂载仅换 token，先使请求代次失效，再确认旧弹窗；旧操作不能继续读取或替换输入。
    await act(async () => {
      root.render(<OfflineWorkbench key="scope-A" durableScopeKey="scope-A" client={client} token="token-B" user={user} isOnline onLog={mocks.log} />);
    });
    await act(async () => { accept(); });
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.getContext).not.toHaveBeenCalled();
    expect(mocks.loadDraft).not.toHaveBeenCalled();
    expect(mocks.listDrafts).not.toHaveBeenCalled();
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.log).not.toHaveBeenCalled();
    expect(container.textContent).toContain("当前评分输入尚未保存");
    await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft.score).toBe(9);
    expect(payloads.has("task-B")).toBe(false);
  });

  it("旧任务包读取收尾不能释放新会话的下载锁", async () => {
    await mount(); await act(async () => { mocks.changeScore!(9); });
    let finishOld!: (value: { task: ReviewTask }) => void;
    let finishNew!: (value: { task: ReviewTask }) => void;
    mocks.getTask.mockImplementationOnce(() => new Promise<{ task: ReviewTask }>((resolve) => { finishOld = resolve; }))
      .mockImplementationOnce(() => new Promise<{ task: ReviewTask }>((resolve) => { finishNew = resolve; }));
    mocks.getContext.mockClear(); mocks.save.mockClear();
    const oldSave = mocks.buttons.get("加密保存草稿")!.onClick;
    await click("下载任务包");
    await act(async () => {
      root.render(<OfflineWorkbench key="scope-A" durableScopeKey="scope-A" client={client} token="token-B" user={user} isOnline onLog={mocks.log} />);
    });
    await click("下载任务包");
    // 新下载取得锁后再完成旧请求；旧 finally 不能让已捕获的保存动作穿过新锁。
    await act(async () => { finishOld({ task: { ...task, revision: 7 } }); });
    expect(mocks.getContext).not.toHaveBeenCalled();
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
    await act(async () => { oldSave(); });
    expect(mocks.save).not.toHaveBeenCalled();
    await act(async () => { finishNew({ task }); });
    expect(mocks.getContext).toHaveBeenCalledTimes(1);
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(false);
    await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft.score).toBe(9);
  });

  it("待确认命令阻止新版冻结材料替换原包和评分", async () => {
    await mount(); await act(async () => { mocks.changeScore!(8); });
    mocks.submit.mockRejectedValueOnce(new Error("响应丢失"));
    await click("同步提交");
    const original = structuredClone(payloads.get("task-A")!);
    mocks.getContext.mockResolvedValue({ context: { task: { ...task, revision: 4 }, question: { id: "question-A", score: 10 }, frozen_rubric: { id: "rubric-new", question_id: "question-A", version: "frozen-v2", status: "active", max_score: 10, points: [{ id: "new-point", description: "新评分点", score: 10 }] } } });
    mocks.confirm.mockClear(); mocks.save.mockClear(); mocks.log.mockClear();
    await click("下载任务包");
    expect(container.textContent).toContain("原评分结果尚待确认，当前保留原任务包与评分");
    expect(container.textContent).toContain("评分标准 frozen-v1");
    expect(container.textContent).not.toContain("评分标准 frozen-v2");
    expect(payloads.get("task-A")).toEqual(original);
    expect(mocks.confirm).not.toHaveBeenCalled();
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.log).not.toHaveBeenCalled();
    expect(mocks.submit).toHaveBeenCalledTimes(1);
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
  });

  it("does not change a pending command or its package while restoring an image", async () => {
    mocks.listSegments.mockResolvedValue({ segments: [{ id: "segment-A", submission_page_id: "page-A" }] });
    mocks.listPages.mockResolvedValue({ pages: [{ id: "page-A", file_asset_id: "file-A" }] });
    await mount(); mocks.submit.mockRejectedValueOnce(new Error("响应丢失"));
    await click("同步提交");
    const original = structuredClone(payloads.get("task-A")!);
    await click("恢复答卷图片");
    expect(payloads.get("task-A")).toEqual(original);
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
    expect(mocks.submit).toHaveBeenCalledTimes(1);
  });

  it("keeps legacy scoring inputs during image recovery without treating their source as verified", async () => {
    mocks.listSegments.mockResolvedValue({ segments: [{ id: "segment-A", submission_page_id: "page-A" }] });
    mocks.listPages.mockResolvedValue({ pages: [{ id: "page-A", file_asset_id: "file-A" }] });
    await mount(); await act(async () => { mocks.changeScore!(9); });
    await click("加密保存草稿");
    delete payloads.get("task-A")!.packageSnapshot.assessmentSource;
    await click("加载"); await click("恢复答卷图片"); await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft.score).toBe(9);
    expect(payloads.get("task-A")?.packageSnapshot.assessmentSource).toBeUndefined();
    await click("同步提交");
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(container.textContent).toContain("旧任务包未记录冻结细则来源");
  });

  it("retains the original package and manual draft when refreshing its frozen context fails", async () => {
    await mount(); await act(async () => { mocks.changeScore!(9); });
    mocks.getContext.mockRejectedValueOnce(new Error("冻结上下文暂不可读"));
    await click("下载任务包"); await click("加密保存草稿");
    expect(payloads.get("task-A")?.draft.score).toBe(9);
    expect(payloads.get("task-A")?.packageSnapshot.assessmentSource).toBe("task_context");
    expect(container.textContent).toContain("原任务包和人工评分已保留");
  });

  it("retains legacy drafts for reading but requires downloading a frozen package before submitting", async () => {
    await mount();
    await click("加密保存草稿");
    const legacy = payloads.get("task-A")!;
    delete legacy.packageSnapshot.assessmentSource;
    await click("加载");
    expect(container.textContent).toContain("旧版细则来源未记录，请重新下载");
    await click("同步提交");
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(container.textContent).toContain("旧任务包未记录冻结细则来源");
  });

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
    expect(mocks.submit).toHaveBeenCalledWith(client, "task-A", expect.objectContaining({ score: 8, expected_revision: 3 }), expect.any(String));
    expect(payloads.get("task-A")?.submissionCommand?.id).toBe(mocks.submit.mock.calls[0][3]);
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
    expect(mocks.getCommand).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.update).not.toHaveBeenCalled();
    expect(container.textContent).toContain("草稿加密保存失败，尚未提交：磁盘已满");
    expect(container.querySelector('[data-tag-color="error"]')?.textContent).toBe("同步失败");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(false);
  });

  it.each([
    { stage: "saved-command loading", failure: new Error("本机离线授权已过期"), message: "本机离线授权已过期" },
    { stage: "API error sanitization", failure: { name: "ApiClientError", code: "access_scope_missing", status: 403 }, message: "当前账号没有访问这些数据的权限。" }
  ])("preserves the pre-persistence failure message for $stage", async ({ failure, message }) => {
    await mount();
    mocks.getTask.mockClear();
    mocks.loadDraft.mockRejectedValueOnce(failure);
    await click("同步提交");
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.getCommand).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.update).not.toHaveBeenCalled();
    expect(container.textContent).toContain(`草稿加密保存失败，尚未提交：${message}`);
    expect(container.querySelector('[data-tag-color="error"]')?.textContent).toBe("同步失败");
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(false);
  });

  it("keeps the pre-persistence failure prefix when answer readability is lost while loading the command", async () => {
    mocks.getContext.mockResolvedValue({ context: { ...taskContext, answer_artifact: { ...taskContext.answer_artifact, raw_answer: "", ocr_text: "" } } });
    await mount();
    mocks.getTask.mockClear();
    let finishLoad!: () => void;
    mocks.loadDraft.mockImplementationOnce(() => new Promise<null>((resolve) => { finishLoad = () => resolve(null); }));
    await click("同步提交");
    // 第一次预检已放行；异步读取期间图片失效后，第二次输入校验必须阻止命令写盘和网络提交。
    await act(async () => container.querySelector("img")!.dispatchEvent(new Event("error")));
    await act(async () => { finishLoad(); });
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.getCommand).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.update).not.toHaveBeenCalled();
    expect(container.textContent).toContain("草稿加密保存失败，尚未提交：当前任务没有可核对的答卷文字或图片，请重新读取任务材料或联系管理员；已禁止提交。");
    expect(container.querySelector('[data-tag-color="error"]')?.textContent).toBe("同步失败");
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(false);
  });

  it("retains the persisted command when saving a resolved conflict fails without a pre-persistence prefix", async () => {
    await mount();
    mocks.getTask.mockResolvedValue({ task: { ...task, revision: 4 } });
    mocks.save.mockImplementationOnce(async (record: OfflineDraftRecord) => {
      rows.set(record.taskId, structuredClone(record));
      payloads.set(record.taskId, structuredClone(record));
    }).mockRejectedValueOnce(new Error("待确认命令状态写入失败"));
    // 首次写盘已保存原命令；清除已解决命令的第二次写盘失败后，仍须先恢复原回执。
    await click("同步提交");
    expect(mocks.save).toHaveBeenCalledTimes(2);
    expect(payloads.get("task-A")?.submissionCommand).toBeDefined();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.update).toHaveBeenCalledWith("task-A", {
      syncStatus: "conflict", syncMessage: "待确认命令状态写入失败"
    }, "scope-A");
    expect(container.textContent).toContain("待确认命令状态写入失败");
    expect(container.textContent).not.toContain("草稿加密保存失败，尚未提交");
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
  });

  it("keeps a rejected receipt failed and frozen when clearing its encrypted command fails", async () => {
    await mount();
    mocks.submit.mockRejectedValueOnce(new Error("响应丢失"));
    await click("同步提交");
    const command = structuredClone(payloads.get("task-A")!.submissionCommand!);
    mocks.save.mockClear();
    mocks.submit.mockClear();
    mocks.getTask.mockClear();
    mocks.update.mockClear();
    mocks.getCommand.mockResolvedValue({ command_id: command.id, status: "rejected" });
    mocks.save.mockImplementationOnce(async (record: OfflineDraftRecord) => {
      rows.set(record.taskId, structuredClone(record));
      payloads.set(record.taskId, structuredClone(record));
    }).mockRejectedValueOnce(new Error("拒绝回执命令清除失败"));
    // 回执已明确拒绝，结果仍为 failed；清除命令的第二次保存失败时保留原载荷供下次核对。
    await click("同步提交");
    expect(mocks.save).toHaveBeenCalledTimes(2);
    expect(mocks.getCommand).toHaveBeenCalledWith(client, command.id);
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(payloads.get("task-A")?.submissionCommand).toEqual(command);
    expect(rows.get("task-A")?.syncStatus).toBe("failed");
    expect(mocks.update).toHaveBeenCalledWith("task-A", { syncStatus: "failed", syncMessage: "拒绝回执命令清除失败" }, "scope-A");
    expect(container.textContent).toContain("拒绝回执命令清除失败");
    expect(container.textContent).not.toContain("草稿加密保存失败，尚未提交");
    expect(container.querySelector('[data-tag-color="error"]')?.textContent).toBe("同步失败");
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
  });

  it("requires an encryption key before offering browser-mode sync", async () => {
    mocks.durable.mockReturnValue(false);
    await mount();
    expect(mocks.buttons.get("同步提交")?.disabled).toBe(true);
    // 已排队的点击到达处理器时也要拦截。
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
    expect(container.textContent).toContain("本地列表刷新失败");
    expect(container.textContent).toContain("本地日志保存失败");
    const text = container.textContent!;
    expect(text.indexOf("本地状态保存失败")).toBeLessThan(text.indexOf("本地列表刷新失败"));
    expect(text.indexOf("本地列表刷新失败")).toBeLessThan(text.indexOf("本地日志保存失败"));
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
    expect(container.textContent).not.toContain("草稿加密保存失败，尚未提交");
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
    // React 重新渲染前捕获的排队回调也会受到拦截。
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
    expect(mocks.loadDraft).toHaveBeenCalledTimes(2);
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
    // 重新打开工作台，让回执只能来自持久化元数据。
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

  it("recovers a lost successful response after reopening without a second POST", async () => {
    await mount();
    // 业务已提交并推进版本，只有响应在传回桌面时丢失；不把网络异常当成服务端回滚。
    mocks.submit.mockImplementationOnce(async () => {
      mocks.getTask.mockResolvedValue({ task: { ...task, revision: 4, status: "completed" } });
      throw new Error("响应丢失");
    });
    await click("同步提交");
    expect(rows.get("task-A")?.syncStatus).toBe("failed");
    const commandId = payloads.get("task-A")!.submissionCommand!.id;
    mocks.getCommand.mockResolvedValue({ command_id: commandId, status: "succeeded", operation: "review.submit", target_id: "task-A", result: { task: { id: "task-A", revision: 4, status: "completed" } } });
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    await mount();
    await click("加载");
    mocks.getTask.mockClear();
    await click("同步提交");
    expect(rows.get("task-A")?.syncStatus).toBe("synced");
    expect(container.textContent).toContain("已从原操作回执确认人工评分");
    expect(mocks.getCommand).toHaveBeenCalledWith(client, commandId);
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.submit).toHaveBeenCalledTimes(1);
  });

  it("recovers success after reopening when local status persistence failed", async () => {
    await mount();
    mocks.update.mockRejectedValueOnce(new Error("本地状态写入失败"));
    await click("同步提交");
    const commandId = payloads.get("task-A")!.submissionCommand!.id;
    expect(rows.get("task-A")?.syncStatus).toBe("syncing");
    mocks.getCommand.mockResolvedValue({ command_id: commandId, status: "succeeded", operation: "review.submit", target_id: "task-A", result: { task: { id: "task-A" } } });
    mocks.getTask.mockResolvedValue({ task: { ...task, revision: 4, status: "completed" } });
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    await mount();
    await click("加载");
    await click("同步提交");
    expect(rows.get("task-A")?.syncStatus).toBe("synced");
    expect(mocks.submit).toHaveBeenCalledTimes(1);
    expect(mocks.getCommand).toHaveBeenCalledWith(client, commandId);
  });

  it("retries an unaccepted command with its frozen key and payload after reopening", async () => {
    await mount();
    await act(async () => { mocks.changeScore!(8); });
    mocks.submit.mockRejectedValueOnce(new Error("响应丢失"));
    await click("同步提交");
    const originalCommand = structuredClone(payloads.get("task-A")!.submissionCommand!);
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    await mount();
    // 即使未加载原草稿并填写了新分数，同步也只恢复原输入。
    await act(async () => { mocks.changeScore!(2); });
    await click("同步提交");
    expect(mocks.getCommand).toHaveBeenCalledWith(client, originalCommand.id);
    expect(mocks.submit).toHaveBeenLastCalledWith(client, "task-A", originalCommand.payload, originalCommand.id);
    expect(payloads.get("task-A")!.draft.score).toBe(8);
    expect(rows.get("task-A")?.syncStatus).toBe("synced");
  });

  it.each(["processing", "unknown"])("keeps a %s command frozen without another POST", async (status) => {
    await mount();
    mocks.submit.mockRejectedValueOnce(new Error("响应丢失"));
    await click("同步提交");
    const original = structuredClone(payloads.get("task-A")!.submissionCommand!);
    mocks.getCommand.mockResolvedValue({ command_id: original.id, status });
    mocks.getTask.mockClear();
    await click("同步提交");
    expect(mocks.submit).toHaveBeenCalledTimes(1);
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(payloads.get("task-A")!.submissionCommand).toEqual(original);
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
    expect(rows.get("task-A")?.syncStatus).toBe("failed");
  });

  it("takes over an expired reservation using the original key without task preflight", async () => {
    await mount();
    mocks.submit.mockRejectedValueOnce(new Error("响应丢失"));
    await click("同步提交");
    const original = structuredClone(payloads.get("task-A")!.submissionCommand!);
    mocks.getCommand.mockResolvedValue({ command_id: original.id, status: "takeover_ready", payload: original.payload });
    mocks.getTask.mockClear();
    await click("同步提交");
    expect(mocks.submit).toHaveBeenLastCalledWith(client, "task-A", original.payload, original.id);
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(rows.get("task-A")?.syncStatus).toBe("synced");
  });

  it("prevents manual save from overwriting a pending command before loading it", async () => {
    await mount();
    mocks.submit.mockRejectedValueOnce(new Error("响应丢失"));
    await click("同步提交");
    const original = structuredClone(payloads.get("task-A")!);
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    await mount();
    await act(async () => { mocks.changeScore!(2); });
    mocks.save.mockClear();
    await click("加密保存草稿");
    expect(mocks.save).not.toHaveBeenCalled();
    expect(payloads.get("task-A")).toEqual(original);
    expect(container.textContent).toContain("原评分结果尚待确认");
    expect(mocks.buttons.get("加密保存草稿")?.disabled).toBe(true);
  });

  it("keeps a legacy encrypted draft readable and attaches a command only at submission", async () => {
    await mount();
    await act(async () => { mocks.changeScore!(7); });
    await click("加密保存草稿");
    expect(payloads.get("task-A")!.submissionCommand).toBeUndefined();
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    await mount();
    await click("加载");
    await click("同步提交");
    expect(mocks.getCommand).not.toHaveBeenCalled();
    expect(mocks.submit).toHaveBeenLastCalledWith(client, "task-A", expect.objectContaining({ score: 7, expected_revision: 3 }), expect.any(String));
    expect(rows.get("task-A")?.syncStatus).toBe("synced");
  });

  it.each(["server", "actor", "tenant"])("refuses to recover a pending command after changing %s", async (identity) => {
    await mount();
    mocks.submit.mockRejectedValueOnce(new Error("响应丢失"));
    await click("同步提交");
    const original = structuredClone(payloads.get("task-A")!);
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    const otherClient = identity === "server" ? new DesktopApiClient({ baseUrl: "https://other.example.edu" }) : client;
    const otherUser = { ...user, id: identity === "actor" ? "other-teacher" : user.id, tenant_id: identity === "tenant" ? "tenant-B" : user.tenant_id };
    await act(async () => { root.render(<OfflineWorkbench key="scope-B" durableScopeKey="scope-B" client={otherClient} token="token-B" user={otherUser} isOnline onLog={mocks.log} />); });
    await click("获取我的任务");
    await click("下载任务包");
    mocks.save.mockClear();
    await act(async () => mocks.changeScore!(6));
    await click("同步提交");
    expect(mocks.getCommand).not.toHaveBeenCalled();
    expect(mocks.submit).toHaveBeenCalledTimes(1);
    expect(mocks.save).not.toHaveBeenCalled();
    expect(payloads.get("task-A")).toEqual(original);
    expect(container.textContent).toContain("草稿加密保存失败，尚未提交：待确认评分属于其他服务器或账号，请切回原账号核对；当前账号不会提交它。");
    expect(container.querySelector('[data-tag-color="error"]')?.textContent).toBe("同步失败");
  });

  it("does not post or write status after unmounting during receipt recovery", async () => {
    await mount();
    mocks.submit.mockRejectedValueOnce(new Error("响应丢失"));
    await click("同步提交");
    const commandId = payloads.get("task-A")!.submissionCommand!.id;
    let finishReceipt!: (value: unknown) => void;
    mocks.getCommand.mockImplementationOnce(() => new Promise((resolve) => { finishReceipt = resolve; }));
    mocks.update.mockClear();
    await click("同步提交");
    await act(async () => { root.unmount(); });
    root = createRoot(container);
    mocks.getTask.mockClear();
    await act(async () => { finishReceipt({ command_id: commandId, status: "not_accepted" }); });
    expect(mocks.submit).toHaveBeenCalledTimes(1);
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.update).not.toHaveBeenCalled();
  });

  it("persists an offline retry without updating a nonexistent draft", async () => {
    await mount(false);
    mocks.getTask.mockClear();
    await click("同步提交");
    expect(rows.get("task-A")).toMatchObject({ syncStatus: "failed", draft: { score: 6 } });
    expect(mocks.getTask).not.toHaveBeenCalled();
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(mocks.update).toHaveBeenCalledWith("task-A", { syncStatus: "failed", syncMessage: "当前未登录或离线，无法同步；加密草稿可稍后重试。" }, "scope-A");
    expect(container.textContent).toContain("当前未登录或离线，无法同步；加密草稿可稍后重试。");
    expect(container.textContent).not.toContain("草稿加密保存失败，尚未提交");
  });

  it("preserves a detected revision conflict without posting a grade", async () => {
    await mount();
    mocks.getTask.mockResolvedValue({ task: { ...task, revision: 4 } });
    await click("同步提交");
    expect(rows.get("task-A")?.syncStatus).toBe("conflict");
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(container.textContent).toContain("服务端任务版本已变化");
  });

  it.each([
    ["submitted", "已提交", "核对服务端评分结果"],
    ["completed", "已完成", "核对服务端评分结果"],
    ["cancelled", "已取消", "核对任务安排"],
    ["withdrawn", "已撤回", "核对任务安排"],
    ["revoked", "已撤回", "核对任务安排"]
  ])("保留 %s 冲突草稿并说明中文状态与下一步，不再提交评分", async (status, label, nextStep) => {
    await mount();
    // 保持下载版本相同，只让服务端任务进入不可提交状态，验证状态提示与提交门禁。
    mocks.getTask.mockResolvedValue({ task: { ...task, status } });
    await click("同步提交");
    expect(mocks.submit).not.toHaveBeenCalled();
    expect(rows.get("task-A")).toMatchObject({ syncStatus: "conflict", draft: { score: 6 } });
    expect(container.textContent).toContain(`服务端任务${label}`);
    expect(container.textContent).toContain("本地草稿仍保留");
    expect(container.textContent).toContain(nextStep);
    expect(container.textContent).not.toContain(`状态为 ${status}`);
  });

  it("handles an initial local listing failure without rejecting the effect", async () => {
    mocks.listDrafts.mockRejectedValue(new Error("本地存储不可用"));
    await act(async () => {
      root.render(<OfflineWorkbench key="scope-A" durableScopeKey="scope-A" client={client} token="token" user={user} isOnline onLog={mocks.log} />);
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
    mocks.listDrafts.mockResolvedValue([{ taskId: "old-synced", anonymousCode: "匿名旧卷", savedAt: "2026-01-01", expiresAt: "2026-02-01", syncStatus: "synced" }]);
    await mount();
    mocks.purge.mockRejectedValueOnce(new Error("磁盘不可用"));
    await click("清理过期已同步草稿");
    expect(container.textContent).toContain("本地缓存清理失败");
    mocks.purge.mockResolvedValueOnce(2);
    mocks.log.mockRejectedValue(new Error("日志写入失败"));
    await click("清理过期已同步草稿");
    expect(container.textContent).toContain("已清理 2 条过期本地缓存，但本地列表或日志更新失败");
  });
});
