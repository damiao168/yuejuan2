// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import type { Grade, School } from "../../../api/org";
import { createSubjectDraft, initialCreateExamDraft } from "./createExamDraft";
import { CreateExamComposer } from "./CreateExamComposer";
import { createExamFieldId } from "./createExamValidation";
import type { CreateExamDraft, ExamCreationMode } from "./types";

vi.mock("framer-motion", () => ({
  AnimatePresence: ({ children }: { children: ReactNode }) => <>{children}</>,
  motion: {
    article: ({ children, className, ...props }: { children: ReactNode; className?: string; "data-exam-field"?: string }) => <article className={className} data-exam-field={props["data-exam-field"]}>{children}</article>,
    div: ({ children, className, ...props }: { children: ReactNode; className?: string; "data-exam-field"?: string }) => <div className={className} data-exam-field={props["data-exam-field"]}>{children}</div>
  }
}));
vi.mock("antd", () => {
  const Radio = Object.assign(({ children }: { children: ReactNode }) => <span>{children}</span>, {
    Group: ({ children }: { children: ReactNode }) => <div>{children}</div>
  });
  return {
    Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean }) =>
      <button type="button" disabled={disabled || loading} onClick={onClick}>{children}</button>,
    Input: ({ id, value, onChange }: { id: string; value: string; onChange: React.ChangeEventHandler<HTMLInputElement> }) => <input id={id} value={value} onChange={onChange} />,
    InputNumber: ({ id, value }: { id: string; value: number }) => <input id={id} type="number" value={value} readOnly />,
    Select: ({ id, value, options, onChange, status }: { id?: string; value?: string; options: Array<{ value: string; label: string }>; onChange: (value: string) => void; status?: string }) =>
      <select id={id} value={value ?? ""} data-status={status} onChange={(event) => onChange(event.target.value)}><option value="" />{options.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select>,
    Checkbox: ({ children }: { children: ReactNode }) => <span>{children}</span>, Radio,
    Drawer: ({ open, children }: { open: boolean; children: ReactNode }) => open ? <aside>{children}</aside> : null
  };
});

const schools: School[] = [
  { id: "school-a", tenant_id: "tenant-a", name: "学校甲", code: "a", status: "active" },
  { id: "school-b", tenant_id: "tenant-a", name: "学校乙", code: "b", status: "active" }
];

describe("考试创建展示与学校范围", () => {
  let host: HTMLDivElement;
  let root: Root;
  let onChange: Mock<(patch: Partial<CreateExamDraft>) => void>;
  let onSubmit: Mock<() => void>;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    onChange = vi.fn<(patch: Partial<CreateExamDraft>) => void>();
    onSubmit = vi.fn<() => void>();
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    host.remove();
  });
  async function render(draft = initialCreateExamDraft("school-a"), availableSchools = schools, submitting = false, availableGrades: Grade[] = []) {
    await act(async () => {
      root.render(<CreateExamComposer draft={draft} schools={availableSchools} grades={availableGrades} classes={[]} templates={[]} submitting={submitting} issues={[]} onChange={onChange} onSubmit={onSubmit} />);
    });
  }
  function submitButton() {
    return host.querySelector<HTMLButtonElement>(".exam-composer-submit button")!;
  }
  async function selectSchool(value: string) {
    await act(async () => {
      const select = host.querySelector<HTMLSelectElement>(`#${createExamFieldId("schoolId")}`)!;
      select.value = value;
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
  }

  it("新建考试不再要求类型，底部摘要只展示年级和参考范围", async () => {
    await render(initialCreateExamDraft("school-a"));
    expect(host.querySelector('[data-exam-field="examType"]')).toBeNull();
    expect(host.textContent).not.toContain("考试类型");
    expect(host.querySelector(".exam-composer-submit span")?.textContent).toBe("未选择年级 · 0 个班级 · 0 个学科");
  });

  it("固定届别显示当前及归档年度，选项和摘要仍绑定原年级ID", async () => {
    const common = { tenant_id: "tenant-a", school_id: "school-a", grade_cohort_id: "cohort-26", entry_year: 2026, education_stage: "senior" as const, status: "active" };
    const availableGrades: Grade[] = [{ ...common, id: "grade-old", name: "高一", level_no: 10, academic_year: "2026-2027" }, { ...common, id: "grade-current", name: "高二", level_no: 11, academic_year: "2027-2028" }];
    await render({ ...initialCreateExamDraft("school-a"), gradeId: "grade-current" }, schools, false, availableGrades);
    const options = [...host.querySelectorAll<HTMLOptionElement>(`#${createExamFieldId("gradeId")} option`)];
    expect(options.map(item => [item.value, item.textContent])).toEqual([["", ""], ["grade-old", "2026级 · 高一（归档 2026-2027）"], ["grade-current", "2026级 · 高二"]]);
    expect(host.querySelector(".exam-composer-submit")?.textContent).toContain("2026级 · 高二");
    expect(onChange).not.toHaveBeenCalled();
  });

  it.each([
    { mode: "materials", label: "创建考试并上传试卷", mobile: "创建并上传" },
    { mode: "template", label: "使用方案创建考试", mobile: "创建考试" },
    { mode: "quick", label: "创建考试", mobile: "创建考试" },
    { mode: "unknown", label: "创建考试", mobile: "创建考试" },
    { mode: "constructor", label: "创建考试", mobile: "创建考试" }
  ])("$mode 保持提交文字和原回调", async ({ mode, label, mobile }) => {
    await render({ ...initialCreateExamDraft("school-a"), creationMode: mode as ExamCreationMode });
    expect(submitButton().textContent).toBe(label);
    expect(host.querySelector(".exam-composer-mobile-action button")?.textContent).toBe(mobile);
    await act(async () => { submitButton().click(); });
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("提交中保留两个入口的 loading 禁用状态", async () => {
    await render(initialCreateExamDraft("school-a"), schools, true);
    expect(submitButton().disabled).toBe(true);
    expect(host.querySelector<HTMLButtonElement>(".exam-composer-mobile-action button")?.disabled).toBe(true);
    await act(async () => { submitButton().click(); });
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("切换学校保持选项顺序并清空全部旧学校范围", async () => {
    const draft: CreateExamDraft = { ...initialCreateExamDraft("school-a"), gradeId: "grade-a", templateId: "template-a", classIds: ["class-a"], subjects: [createSubjectDraft("math")] };
    await render(draft);
    const select = host.querySelector<HTMLSelectElement>(`#${createExamFieldId("schoolId")}`)!;
    expect([...select.options].map((item) => item.value)).toEqual(["", "school-a", "school-b"]);
    await selectSchool("school-b");
    expect(onChange).toHaveBeenCalledOnce();
    expect(onChange).toHaveBeenCalledWith({ schoolId: "school-b", gradeId: "", templateId: "", classIds: [], subjects: [] });
    expect(draft.gradeId).toBe("grade-a");
    expect(draft.subjects[0].subject).toBe("math");
  });

  it.each([
    { available: [schools[0]], schoolId: "school-a", label: "学校甲" },
    { available: [schools[0]], schoolId: "missing", label: "尚未选择" },
    { available: [], schoolId: "", label: "尚未选择" },
    { available: [{ ...schools[0], name: "" }], schoolId: "school-a", label: "" }
  ])("单学校或缺失学校保留显示“$label”", async ({ available, schoolId, label }) => {
    await render(initialCreateExamDraft(schoolId), available);
    const field = host.querySelector(".exam-composer-school")!;
    expect(field.querySelector("strong")?.textContent).toBe(label);
    expect(field.querySelector("select")).toBeNull();
  });

  it("原生科目分组保留增删顺序、已选草稿引用和按钮语义", async () => {
    const math = { ...createSubjectDraft("math"), totalScore: 150 };
    const chinese = createSubjectDraft("chinese");
    const draft = { ...initialCreateExamDraft("school-a"), subjects: [math, chinese] };
    await render(draft);
    const group = host.querySelector<HTMLFieldSetElement>("fieldset.exam-subject-picker")!;
    expect(group.getAttribute("aria-label")).toBe("选择考试科目");
    expect(group.hasAttribute("role")).toBe(false);
    expect(group.disabled).toBe(false);
    expect(group.hasAttribute("form")).toBe(false);
    expect(group.hasAttribute("name")).toBe(false);
    const button = (text: string) => [...group.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === text)!;
    expect(button("数学").type).toBe("button");
    expect(button("数学").getAttribute("aria-pressed")).toBe("true");
    expect(button("英语").getAttribute("aria-pressed")).toBe("false");
    await act(async () => { button("英语").click(); });
    const added = onChange.mock.calls[0][0].subjects as CreateExamDraft["subjects"];
    expect(added.map((item) => item.subject)).toEqual(["math", "chinese", "english"]);
    expect(added[0]).toBe(math);
    expect(added[1]).toBe(chinese);
    expect(added[2]).toEqual({ subject: "english", totalScore: 100, durationMinutes: 90, candidateRule: "all_selected_classes", classIds: [], sections: [] });
    await act(async () => { button("数学").click(); });
    const removed = onChange.mock.calls[1][0].subjects as CreateExamDraft["subjects"];
    expect(removed).toEqual([chinese]);
    expect(removed[0]).toBe(chinese);
    expect(draft.subjects).toEqual([math, chinese]);
  });
});
