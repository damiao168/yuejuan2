// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperImportDraftQuestion, PaperImportIssue, PaperImportJob, PaperImportSource } from "../../api/papers";
import { PaperImportWorkspace } from "./PaperImportWorkspace";
import { importSourceDescription, importSourceStatus, importVisibleIssues } from "./paperImportPresentation";
import type { usePaperImportWorkflow } from "./usePaperImportWorkflow";

vi.mock("antd", () => {
  const Button = ({ children, disabled, onClick, "aria-label": label }: { children?: ReactNode; disabled?: boolean; onClick?: () => void; "aria-label"?: string }) =>
    <button disabled={disabled} onClick={onClick} aria-label={label}>{children}</button>;
  const Item = Object.assign(({ children, actions, extra }: { children?: ReactNode; actions?: ReactNode[]; extra?: ReactNode }) =>
    <li>{children}{actions}{extra}</li>, {
    Meta: ({ title, description }: { title: string; description: string }) => <div>{title}{description}</div>
  });
  const List = Object.assign(({ dataSource, renderItem }: { dataSource: PaperImportSource[]; renderItem: (source: PaperImportSource, index: number) => ReactNode }) =>
    <ul>{dataSource.map((source, index) => <div key={source.id}>{renderItem(source, index)}</div>)}</ul>, { Item });
  return {
    Button, List,
    Alert: ({ message, description }: { message: ReactNode; description: ReactNode }) => <aside>{message}{description}</aside>,
    Upload: { Dragger: ({ children, disabled }: { children: ReactNode; disabled: boolean }) => <div data-upload-disabled={disabled}>{children}</div> },
    Select: ({ id, value, options, onChange, disabled, "aria-label": label }: {
      id?: string; value: string; options: { value: string; label: string }[]; onChange: (value: string) => void; disabled?: boolean; "aria-label"?: string;
    }) => <select id={id} value={value} onChange={(event) => onChange(event.target.value)} disabled={disabled} aria-label={label}>
      {options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
    </select>,
    Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
    Progress: ({ percent }: { percent: number }) => <progress value={percent} max={100} />,
    Modal: ({ open, children, onOk, onCancel, okButtonProps }: {
      open: boolean; children: ReactNode; onOk: () => void; onCancel: () => void; okButtonProps: { disabled: boolean };
    }) => open ? <div role="dialog">{children}<Button disabled={okButtonProps.disabled} onClick={onOk}>添加并识别</Button><Button onClick={onCancel}>取消</Button></div> : null,
    Input: { TextArea: ({ value, onChange }: { value: string; onChange: (event: { target: { value: string } }) => void }) =>
      <textarea value={value} onChange={onChange} /> },
    Tabs: ({ activeKey, onChange, items }: { activeKey: string; onChange: (key: string) => void; items: { key: string; label: string; children: ReactNode }[] }) =>
      <div>{items.map((item) => <button key={item.key} role="tab" aria-selected={activeKey === item.key} onClick={() => onChange(item.key)}>{item.label}</button>)}
        {items.find((item) => item.key === activeKey)?.children}</div>
  };
});
vi.mock("../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: ReactNode }) => <div>{children}</div> }));
vi.mock("./PaperImportReviewPanel", () => ({ PaperImportReviewPanel: ({ readOnly }: { readOnly: boolean }) => <div data-review-readonly={readOnly}>识别草稿</div> }));

function source(overrides: Partial<PaperImportSource> = {}): PaperImportSource {
  return { id: "source-1", file_asset_id: "file-1", document_index: 0, role_hint: "auto", detected_role: "answer", role_confidence: 0.91, processing_status: "processed", ...overrides };
}

function job(status: PaperImportJob["status"], overrides: Partial<PaperImportJob> = {}): PaperImportJob {
  return { id: "import-1", exam_id: "exam-1", generation: 7, status, sources: [source()], questions: [], structured_issues: [], issues: [], ...overrides } as PaperImportJob;
}

function issue(code: string): PaperImportIssue {
  return { code, severity: "warning", certainty: "suspected", message: code, source_refs: [] };
}

describe("paper import presentation", () => {
  it("hides previous issues while processing or stopped and restricts no-content warnings", () => {
    const issues = [issue("MISSING_ANSWER"), issue("NO_EXAM_CONTENT_DETECTED")];
    expect(importVisibleIssues(job("processing", { structured_issues: issues }))).toEqual([]);
    expect(importVisibleIssues(job("failed", { error_code: "paper_import_cancelled", structured_issues: issues }))).toEqual([]);
    expect(importVisibleIssues(job("review_required", { structured_issues: issues }))).toEqual([issues[1]]);
    expect(importVisibleIssues(job("failed", { issues: ["检查资料"] }))).toEqual([{ message: "检查资料", certainty: "unknown" }]);
  });

  it("retains source status labels and does not present role confidence as text accuracy", () => {
    expect(importSourceStatus(source(), true)).toEqual({ tone: "neutral", label: "已停止" });
    expect(importSourceStatus(source(), false)).toEqual({ tone: "success", label: "已识别" });
    expect(importSourceStatus(source({ processing_status: "failed" }), false)).toEqual({ tone: "danger", label: "失败" });
    expect(importSourceStatus(source({ processing_status: "pending" }), false)).toEqual({ tone: "processing", label: "处理中" });
    expect(importSourceDescription(source())).toBe("识别内容：答案 · 资料类型判断 91%（不代表逐字准确率）");
    expect(importSourceDescription(source({ role_confidence: 0, detected_role: "unknown" }))).toBe("识别内容：识别中");
  });
});

describe("paper import workspace", () => {
  let root: Root;
  let container: HTMLDivElement;
  let workflow: ReturnType<typeof usePaperImportWorkflow>;
  let onNavigate: ReturnType<typeof vi.fn>;
  let showEditor: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    onNavigate = vi.fn();
    showEditor = vi.fn();
    workflow = {
      uploadProps: {}, parsing: false, reviewDrafts: [], savingImportReview: false, updatingImportSources: false,
      stoppingImport: false, retryingParse: false, invalidReviewRubric: false, invalidReviewScore: false,
      importPastedText: vi.fn().mockResolvedValue(false), replaceImportSources: vi.fn(), removeImportSource: vi.fn(),
      openImportSource: vi.fn(), retryImportParse: vi.fn(), stopPaperImport: vi.fn(), saveImportReview: vi.fn(),
      confirmPaperImport: vi.fn(), updateReviewDraft: vi.fn()
    } as unknown as ReturnType<typeof usePaperImportWorkflow>;
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });

  async function mount(latestPaperImport?: PaperImportJob, canManage = true, navigate = onNavigate) {
    await act(async () => { root.render(<PaperImportWorkspace canManage={canManage} latestPaperImport={latestPaperImport}
      workflow={workflow} setShowQuestionEditor={showEditor} initialExamId="exam /1" onNavigate={navigate} />); });
  }

  function button(text: string) {
    const result = Array.from(container.querySelectorAll("button")).find((item) => item.textContent === text);
    expect(result, text).toBeDefined();
    return result!;
  }

  async function click(text: string) {
    await act(async () => { button(text).click(); });
  }

  async function setText(value: string) {
    const textarea = container.querySelector("textarea")!;
    const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!;
    await act(async () => { setter.call(textarea, value); textarea.dispatchEvent(new Event("input", { bubbles: true })); });
  }

  it("preserves text, role and preview on cancel or failed submission and clears them only after success", async () => {
    await mount();
    await click("粘贴文本");
    const text = "## 第 1 题\n已知函数 f(x)=x^2+2x+1，求最小值。";
    await setText(text);
    await act(async () => {
      const select = container.querySelector<HTMLSelectElement>("#paper-text-import-role")!;
      select.value = "answer";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await click("公式预览");
    await click("取消");
    expect(container.querySelector("[role=dialog]")).toBeNull();
    await click("粘贴文本");
    expect(button("公式预览").getAttribute("aria-selected")).toBe("true");
    expect(container.querySelector<HTMLSelectElement>("#paper-text-import-role")?.value).toBe("answer");
    expect(container.querySelector(".paper-text-import-preview")?.textContent).toBe(text);
    await click("添加并识别");
    expect(workflow.importPastedText).toHaveBeenCalledWith(text, "answer");
    expect(container.querySelector("[role=dialog]")).not.toBeNull();
    expect(container.querySelector(".paper-text-import-preview")?.textContent).toBe(text);
    vi.mocked(workflow.importPastedText).mockResolvedValueOnce(true);
    await click("添加并识别");
    expect(container.querySelector("[role=dialog]")).toBeNull();
    await click("粘贴文本");
    expect(container.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("");
    expect(container.querySelector<HTMLSelectElement>("#paper-text-import-role")?.value).toBe("auto");
    expect(button("编辑").getAttribute("aria-selected")).toBe("true");
  });

  it("keeps paste and upload disabled for read-only users and during parsing", async () => {
    await mount(undefined, false);
    expect(button("粘贴文本").disabled).toBe(true);
    expect(container.querySelector("[data-upload-disabled]")?.getAttribute("data-upload-disabled")).toBe("true");
    workflow.parsing = true;
    await mount();
    expect(button("粘贴文本").disabled).toBe(true);
  });

  it("retains drafts as read-only while processing and exposes native indeterminate progress", async () => {
    workflow.reviewDrafts = [{ question_no: "1", score: 2 }] as PaperImportDraftQuestion[];
    const active = job("processing", { structured_issues: [issue("MISSING_ANSWER")], questions: workflow.reviewDrafts });
    await mount(active);
    expect(container.querySelector("[data-review-readonly]")?.getAttribute("data-review-readonly")).toBe("true");
    expect(container.textContent).toContain("上一轮已识别的 1 道题目仍然保留");
    expect(container.textContent).not.toContain("MISSING_ANSWER");
    expect(container.querySelector("progress")?.hasAttribute("value")).toBe(false);
    await click("停止识别");
    expect(workflow.stopPaperImport).toHaveBeenCalledWith(active);
  });

  it("排队无阶段记录时显示真实任务更新时间，刷新展示仍不冒充已处理比例", async () => {
    const updatedAt = "2026-10-06T10:00:07Z";
    const active = job("processing", { sources: [source({ processing_status: "pending" })], updated_at: updatedAt });
    await mount(active);
    expect(container.textContent).toContain(`最近更新：${new Date(updatedAt).toLocaleTimeString("zh-CN", { hour12: false })}`);
    expect(container.textContent).toContain("等待识别");
    expect(container.querySelector("progress")?.hasAttribute("value")).toBe(false);
    await mount({ ...active });
    expect(container.textContent).toContain(`最近更新：${new Date(updatedAt).toLocaleTimeString("zh-CN", { hour12: false })}`);
    expect(container.querySelector("progress")?.hasAttribute("value")).toBe(false);
  });

  it.each([
    ["failed", "ai_parse_failed", "仅重新解析"],
    ["cancelled", "", "仅解析本次补充"]
  ] as const)("preserves the retry-parse and retry-all actions for %s", async (status, errorCode, label) => {
    const active = job(status, { error_code: errorCode });
    await mount(active);
    await click(label);
    expect(workflow.retryImportParse).toHaveBeenCalledWith(active);
    await click("重新识别全部");
    expect(workflow.replaceImportSources).toHaveBeenCalledWith(active, active.sources);
  });

  it("retains confirmed-generation routing and leaves another generation available for review", async () => {
    workflow.reviewDrafts = [{ question_no: "1", score: 2 }] as PaperImportDraftQuestion[];
    const active = job("review_required", { structure_confirmed_generation: 7, questions: workflow.reviewDrafts, structured_issues: [issue("MISSING_ANSWER")] });
    await mount(active);
    expect(container.querySelector("[data-review-readonly]")).toBeNull();
    expect(container.textContent).not.toContain("MISSING_ANSWER");
    await click("核对小题与分值");
    expect(onNavigate).toHaveBeenCalledWith("/exams/exam%20%2F1/questions");
    expect(showEditor).not.toHaveBeenCalled();
    await mount({ ...active, generation: 8 });
    expect(container.querySelector("[data-review-readonly]")?.getAttribute("data-review-readonly")).toBe("false");
    expect(container.textContent).toContain("确认导入");
  });

  it("preserves source changes and hides import confirmation when the job contains question material", async () => {
    const active = job("review_required", { sources: [source({ detected_role: "question" }), source({ id: "source-2", file_asset_id: "file-2", document_index: 1 })] });
    await mount(active);
    expect(container.textContent).not.toContain("确认导入");
    const upButtons = container.querySelectorAll<HTMLButtonElement>("button[aria-label='上移资料']");
    expect(upButtons[0].disabled).toBe(true);
    await act(async () => { upButtons[1].click(); });
    expect(workflow.replaceImportSources).toHaveBeenCalledWith(active, [
      { ...active.sources[1], document_index: 0 }, { ...active.sources[0], document_index: 1 }
    ]);
    expect(active.sources[0].id).toBe("source-1");
  });

  it("preserves role changes and removal arguments without changing the displayed source", async () => {
    const active = job("review_required");
    await mount(active);
    await act(async () => {
      const select = container.querySelector<HTMLSelectElement>("select[aria-label='资料类型']")!;
      select.value = "solution";
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(workflow.replaceImportSources).toHaveBeenCalledWith(active, [{ ...active.sources[0], role_hint: "solution" }]);
    expect(active.sources[0].role_hint).toBe("auto");
    await act(async () => { container.querySelector<HTMLButtonElement>("button[aria-label='删除资料']")!.click(); });
    expect(workflow.removeImportSource).toHaveBeenCalledWith(active, "source-1");
    workflow.updatingImportSources = true;
    await mount(active);
    expect(container.querySelector<HTMLSelectElement>("select[aria-label='资料类型']")?.disabled).toBe(true);
    expect(container.querySelector<HTMLButtonElement>("button[aria-label='删除资料']")?.disabled).toBe(true);
  });

  it("routes no-content and non-parse failures to their existing actions", async () => {
    const active = job("review_required", { structured_issues: [issue("NO_EXAM_CONTENT_DETECTED"), issue("MISSING_ANSWER")] });
    await mount(active);
    expect(container.textContent).toContain("未识别到考试内容");
    expect(container.textContent).not.toContain("MISSING_ANSWER");
    await click("重新识别");
    expect(workflow.replaceImportSources).toHaveBeenCalledWith(active, active.sources);
    await click("手动补充题目");
    expect(onNavigate).toHaveBeenCalled();
    await mount(job("failed"));
    expect(container.textContent).toContain("重新识别");
    await mount(job("failed", { sources: [] }));
    expect(container.textContent).not.toContain("重新识别");
  });

  it("keeps existing confirmation validation and applied-result source controls", async () => {
    const active = job("review_required", { questions: [{ question_no: "1", score: 2 }] as PaperImportDraftQuestion[] });
    workflow.invalidReviewScore = true;
    await mount(active);
    expect(button("确认导入").disabled).toBe(true);
    workflow.invalidReviewScore = false;
    await mount(active);
    await click("保存草稿");
    await click("确认导入");
    expect(workflow.saveImportReview).toHaveBeenCalledWith(active);
    expect(workflow.confirmPaperImport).toHaveBeenCalledWith(active);
    await mount(job("applied"));
    expect(container.textContent).toContain("已确认并写入当前考试");
    expect(container.querySelector<HTMLSelectElement>("select[aria-label='资料类型']")?.disabled).toBe(true);
    expect(container.querySelector<HTMLButtonElement>("button[aria-label='删除资料']")?.disabled).toBe(true);
    expect(container.querySelector("[data-review-readonly]")).toBeNull();
  });
});
