// @vitest-environment jsdom
import { act, lazy, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ExamWorkspaceProjection } from "../../../api/workspace";
import type { ExamReadiness, ReadinessCheck } from "../../../api/configuration";
import { ExamWorkspaceLayout } from "./ExamWorkspaceLayout";

const { getExamReadiness } = vi.hoisted(() => ({ getExamReadiness: vi.fn() }));
vi.mock("../../../api/configuration", () => ({ getExamReadiness }));

vi.mock("../../school-assistant/SchoolAssistantProvider", () => ({ useSchoolAssistantContext: () => null }));
vi.mock("@edugrade/ui", () => ({
  WorkspaceLayout: ({ header, children }: { header: ReactNode; children: ReactNode }) => <>{header}{children}</>,
  ExamStageIndicator: ({ stage }: { stage?: { label: string } }) => <span aria-label="当前考试阶段">{stage?.label}</span>, QualityIndicator: () => null,
  StatusBadge: ({ children }: { children: ReactNode }) => <span>{children}</span>
}));
vi.mock("antd", () => ({
  Button: ({ children, onClick }: { children: ReactNode; onClick: () => void }) => <button onClick={onClick}>{children}</button>,
  Select: ({ value, options, onChange, "aria-label": label }: { value: string; options: { value: string; label: string }[]; onChange: (value: string) => void; "aria-label": string }) => <select aria-label={label} value={value} onChange={(event) => onChange(event.target.value)}>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>,
  Alert: ({ message }: { message: string }) => <p>{message}</p>,
  Spin: () => <span aria-label="正在加载" />
}));

function workspace(overrides: Partial<ExamWorkspaceProjection> = {}): ExamWorkspaceProjection {
  return {
    exam_id: "math", exam_name: "期中数学", exam_session_id: "session 1", exam_session_name: "期中考试", exam_status: "draft", revision: 2,
    stage: "prepare", stages: [], stage_progress: [], blockers: [], warnings: [], counts: { paper_count: 0, question_count: 0, submission_count: 0, failed_submission_count: 0, quality_issue_submission_count: 0, unmatched_submission_count: 0, pending_review_count: 0, pending_arbitration_count: 0 },
    next_actions: [], risk_tier: "unknown", updated_at: "2026-10-04T00:00:00Z", subject_summary: { code: "math", label: "数学", total_score: 150, question_count: 0, configured_question_count: 0, frozen_question_count: 0, risk_tier_source: "none", question_types: {} }, ...overrides
  };
}

function readiness(overrides: Partial<ExamReadiness> = {}): ExamReadiness {
  const sections = {
    students: ["student_scope"], paper: ["paper_file"],
    questions: ["questions", "total_score", "question_scores", "answer_keys", "rubrics"],
    template: ["locked_template", "template_coverage"]
  };
  const checks = Object.entries(sections).flatMap(([section, codes]) => codes.map((code): ReadinessCheck => ({ code, section, label: code, passed: true, severity: "blocker", message: "已通过" })));
  return { ready: true, confirmed: false, configuration_hash: "current-config", import_snapshot_available: false, checks, ...overrides };
}

beforeEach(() => { getExamReadiness.mockReset(); getExamReadiness.mockResolvedValue({ readiness: readiness() }); });

async function render(data: ExamWorkspaceProjection, section = "students", canReadReadiness = true) {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  const navigate = vi.fn();
  const update = async (next: ExamWorkspaceProjection, nextSection = section, nextCanReadReadiness = canReadReadiness, moduleContent: ReactNode = <p>学生范围</p>) => {
    await act(async () => root.render(<ExamWorkspaceLayout data={next} section={nextSection} moduleContent={moduleContent} onNavigate={navigate} canReadReadiness={nextCanReadReadiness} />));
  };
  await update(data);
  return { container, navigate, update, close: async () => { await act(async () => root.unmount()); container.remove(); } };
}

function preparationButtons(container: HTMLElement) {
  return workflowButtons(container).slice(0, 5);
}

function workflowButtons(container: HTMLElement) {
  return [...container.querySelectorAll<HTMLButtonElement>('[aria-label="考试准备与答卷导入"] button')];
}

describe("学科工作台整场上下文", () => {
  it("多科工作台返回服务端整场 ID，单科历史考试仍返回独立总览", async () => {
    for (const [data, expected] of [[workspace(), "/exams/overview/session/session%201"], [workspace({ exam_session_id: undefined }), "/exams/overview/standalone/math"]] as const) {
      const view = await render(data);
      try {
        const button = [...view.container.querySelectorAll("button")].find((item) => item.textContent === "整场总览")!;
        await act(async () => button.click());
        expect(view.navigate).toHaveBeenCalledWith(expected);
        expect(view.container.textContent).toContain("数学 · 150 分");
      } finally { await view.close(); }
    }
  });

  it("无法核实归档状态时显示当前只读，不把故障断言为已归档", async () => {
    const view = await render(workspace({ read_only: true, warnings: [{ code: "archive_state_unavailable", title: "归档状态不可用", message: "暂无法确认", severity: "warning", action_label: "刷新", action_route: "/exams" }] }));
    try {
      expect(view.container.textContent).toContain("归档状态暂无法确认，当前学科只读");
      expect(view.container.textContent).not.toContain("考试已归档");
    } finally { await view.close(); }
  });
  it("准备与学生答卷统一使用六步入口，移除环节下拉和重复状态", async () => {
    const data = workspace({ next_actions: [{ code: "review_import", label: "核对考试资料", description: "导入内容需要人工确认", route: "/exams/math/paper?import=review", priority: "high" }] });
    const view = await render(data);
    try {
      expect(view.container.querySelector('[aria-label="当前考试阶段"]')).toBeNull();
      expect(view.container.querySelector('[aria-label="切换考试环节"]')).toBeNull();
      expect(view.container.querySelector('.exam-preparation-status')).toBeNull();
      expect(view.container.querySelectorAll('[aria-label="考试准备与答卷导入"]')).toHaveLength(1);
      expect(view.container.querySelector('header [aria-label="考试准备与答卷导入"]')).not.toBeNull();
      expect(workflowButtons(view.container).map(button => button.querySelector('span:last-child')?.textContent)).toEqual(["学生范围", "考试资料", "小题与分值", "答题卡设置", "确认准备完成", "学生答卷"]);
      expect(view.container.querySelector('[aria-label="当前下一步"]')).toBeNull();
      const buttons = preparationButtons(view.container);
      expect(buttons).toHaveLength(5);
      await act(async () => buttons[1].click());
      expect(view.navigate).toHaveBeenCalledWith("/exams/math/paper");
    } finally { await view.close(); }
  });

  it("答卷导入通过顶部步骤导航，不重复显示下一步待办", async () => {
    const view = await render(workspace({ next_actions: [{ code: "retry", label: "重试识别", description: "处理异常", route: "/exams/math/processing", priority: "high" }] }), "capture");
    try {
      expect(view.container.querySelector('[aria-label="当前下一步"]')).toBeNull();
      expect(view.container.textContent).not.toContain("重试识别");
      expect(getExamReadiness).toHaveBeenCalledOnce();
      expect(workflowButtons(view.container)[5].getAttribute("aria-current")).toBe("step");
    } finally { await view.close(); }
  });

  it("完成颜色只按真实检查判断，当前位置和ready都不能代替确认", async () => {
    const result = readiness();
    result.checks.find((check) => check.code === "paper_file")!.passed = false;
    getExamReadiness.mockResolvedValue({ readiness: result });
    const view = await render(workspace(), "settings");
    try {
      const buttons = preparationButtons(view.container);
      expect(buttons.map((button) => button.classList.contains("is-complete"))).toEqual([true, false, true, true, false]);
      expect(buttons[1].getAttribute("aria-label")).toContain("未完成");
      expect(buttons[1].title).toContain("paper_file");
      expect(buttons[4].getAttribute("aria-current")).toBe("step");
      expect(buttons[4].title).toContain("仍需核对并确认");
    } finally { await view.close(); }
  });

  it("资料结构待人工采用时，文件已上传也不能把资料和题目标为完成", async () => {
    const result = readiness();
    result.checks.push({ code: "material_structure", section: "questions", label: "试卷资料与题目结构", passed: false, severity: "blocker", message: "请核对采用结构" });
    getExamReadiness.mockResolvedValue({ readiness: result });
    const view = await render(workspace(), "paper");
    try {
      const buttons = preparationButtons(view.container);
      expect(buttons[1].classList.contains("is-incomplete")).toBe(true);
      expect(buttons[2].classList.contains("is-incomplete")).toBe(true);
      expect(buttons[1].title).toContain("试卷资料与题目结构");
    } finally { await view.close(); }
  });

  it("缺失检查数据保持未知灰色，真实已确认也不会给其他四步补造完成状态", async () => {
    getExamReadiness.mockResolvedValue({ readiness: readiness({ checks: [], confirmed: true }) });
    const view = await render(workspace());
    try {
      const buttons = preparationButtons(view.container);
      expect(buttons.slice(0, 4).every((button) => button.classList.contains("is-unknown"))).toBe(true);
      expect(buttons.slice(0, 4).every((button) => button.getAttribute("aria-label")?.includes("状态待检查"))).toBe(true);
      expect(buttons[4].classList.contains("is-complete")).toBe(true);
    } finally { await view.close(); }
  });

  it("题目检查只返回部分通过项时仍待核对，不会把缺失检查当通过", async () => {
    const result = readiness();
    result.checks = result.checks.filter((check) => check.code !== "rubrics");
    getExamReadiness.mockResolvedValue({ readiness: result });
    const view = await render(workspace());
    try { expect(preparationButtons(view.container)[2].classList.contains("is-unknown")).toBe(true); }
    finally { await view.close(); }
  });

  it("后续阅卷阶段以匹配当前配置的真实确认快照展示确认完成", async () => {
    getExamReadiness.mockResolvedValue({ readiness: readiness({ confirmed: false, snapshot_id: "passed-snapshot", confirmed_at: "2026-10-12T01:00:00Z" }) });
    const view = await render(workspace({ exam_status: "grading" }));
    try { expect(preparationButtons(view.container)[4].classList.contains("is-complete")).toBe(true); }
    finally { await view.close(); }
  });

  it("读取失败显示暂无法获取，不能把接口失败声明为未完成", async () => {
    getExamReadiness.mockRejectedValue(new Error("unavailable"));
    const view = await render(workspace());
    try {
      const buttons = preparationButtons(view.container);
      expect(buttons.every((button) => button.classList.contains("is-unknown"))).toBe(true);
      expect(buttons.every((button) => button.title.includes("暂无法获取"))).toBe(true);
      expect(view.container.querySelector('output')).toBeNull();
    } finally { await view.close(); }
  });

  it("无准备检查读取权限时不发送请求，步骤保持待检查", async () => {
    const view = await render(workspace(), "students", false);
    try {
      expect(getExamReadiness).not.toHaveBeenCalled();
      expect(preparationButtons(view.container).every((button) => button.classList.contains("is-unknown"))).toBe(true);
    } finally { await view.close(); }
  });

  it("换学科后旧请求返回不能给新学科标绿", async () => {
    let resolveOld!: (value: { readiness: ExamReadiness }) => void;
    getExamReadiness.mockImplementationOnce(() => new Promise((resolve) => { resolveOld = resolve; }));
    getExamReadiness.mockResolvedValueOnce({ readiness: readiness({ checks: [] }) });
    const view = await render(workspace());
    try {
      expect(preparationButtons(view.container).every((button) => button.classList.contains("is-unknown"))).toBe(true);
      await view.update(workspace({ exam_id: "english" }));
      await act(async () => resolveOld({ readiness: readiness({ confirmed: true }) }));
      expect(getExamReadiness).toHaveBeenLastCalledWith("english");
      expect(preparationButtons(view.container).slice(0, 4).every((button) => button.classList.contains("is-unknown"))).toBe(true);
      expect(preparationButtons(view.container)[4].classList.contains("is-incomplete")).toBe(true);
    } finally { await view.close(); }
  });

  it("同学科保存刷新后重新核对，不能保留过期绿色", async () => {
    getExamReadiness.mockResolvedValueOnce({ readiness: readiness({ confirmed: true }) });
    getExamReadiness.mockResolvedValueOnce({ readiness: readiness({ checks: [] }) });
    const view = await render(workspace());
    try {
      expect(preparationButtons(view.container).every((button) => button.classList.contains("is-complete"))).toBe(true);
      await view.update(workspace({ revision: 3, updated_at: "2026-10-12T02:00:00Z" }));
      expect(getExamReadiness).toHaveBeenCalledTimes(2);
      expect(preparationButtons(view.container).slice(0, 4).every((button) => button.classList.contains("is-unknown"))).toBe(true);
    } finally { await view.close(); }
  });

  it("同一配置切换五个步骤时保留导航节点和完成颜色，只更新当前选中项", async () => {
    const view = await render(workspace());
    try {
      const nav = view.container.querySelector('[aria-label="考试准备与答卷导入"]');
      const buttons = preparationButtons(view.container);
      const initialClasses = buttons.map((button) => button.className.replace(" is-active", ""));
      for (const [index, section] of ["paper", "questions", "template", "settings", "students"].entries()) {
        // 新投影对象内容未变也不应重新核对；导航节点必须在模块切换期间保持原位。
        await view.update(workspace(), section);
        const current = preparationButtons(view.container);
        expect(view.container.querySelector('[aria-label="考试准备与答卷导入"]')).toBe(nav);
        expect(current.every((button, position) => button === buttons[position])).toBe(true);
        expect(current.map((button) => button.className.replace(" is-active", ""))).toEqual(initialClasses);
        expect(current[(index + 1) % 5].getAttribute("aria-current")).toBe("step");
        expect(view.container.querySelector('output')).toBeNull();
        expect(getExamReadiness).toHaveBeenCalledTimes(1);
      }
    } finally { await view.close(); }
  });

  it("首次检查尚未返回时切换步骤共用原请求，返回结果用于当前步骤导航", async () => {
    let resolveReadiness!: (value: { readiness: ExamReadiness }) => void;
    getExamReadiness.mockImplementationOnce(() => new Promise((resolve) => { resolveReadiness = resolve; }));
    const view = await render(workspace());
    try {
      await view.update(workspace(), "questions");
      await view.update(workspace(), "template");
      expect(getExamReadiness).toHaveBeenCalledTimes(1);
      expect(preparationButtons(view.container).every((button) => button.classList.contains("is-unknown"))).toBe(true);
      await act(async () => resolveReadiness({ readiness: readiness() }));
      const buttons = preparationButtons(view.container);
      expect(buttons.slice(0, 4).every((button) => button.classList.contains("is-complete"))).toBe(true);
      expect(buttons[3].getAttribute("aria-current")).toBe("step");
    } finally { await view.close(); }
  });

  it("真实配置更新立即清除旧颜色，等待新检查时切换步骤不会重复请求", async () => {
    let resolveReadiness!: (value: { readiness: ExamReadiness }) => void;
    getExamReadiness.mockResolvedValueOnce({ readiness: readiness({ confirmed: true }) });
    getExamReadiness.mockImplementationOnce(() => new Promise((resolve) => { resolveReadiness = resolve; }));
    const view = await render(workspace());
    try {
      const next = workspace({ revision: 3, updated_at: "2026-10-12T02:00:00Z" });
      await view.update(next, "questions");
      expect(preparationButtons(view.container).every((button) => button.classList.contains("is-unknown"))).toBe(true);
      await view.update(next, "paper");
      expect(getExamReadiness).toHaveBeenCalledTimes(2);
      const result = readiness();
      result.checks.find((check) => check.code === "student_scope")!.passed = false;
      await act(async () => resolveReadiness({ readiness: result }));
      expect(preparationButtons(view.container)[0].classList.contains("is-incomplete")).toBe(true);
      expect(preparationButtons(view.container)[1].getAttribute("aria-current")).toBe("step");
    } finally { await view.close(); }
  });

  it("同版本投影重读期间保留完成颜色，检查返回后按最新结果更新", async () => {
    let resolveReadiness!: (value: { readiness: ExamReadiness }) => void;
    getExamReadiness.mockResolvedValueOnce({ readiness: readiness() });
    getExamReadiness.mockImplementationOnce(() => new Promise((resolve) => { resolveReadiness = resolve; }));
    const view = await render(workspace());
    try {
      const nav = view.container.querySelector('[aria-label="考试准备与答卷导入"]');
      const next = workspace({ updated_at: "2026-10-12T02:00:00Z" });
      await view.update(next, "paper");
      expect(preparationButtons(view.container)[0].classList.contains("is-complete")).toBe(true);
      expect(view.container.querySelector('output')).toBeNull();
      await view.update(next, "questions");
      expect(getExamReadiness).toHaveBeenCalledTimes(2);
      expect(view.container.querySelector('[aria-label="考试准备与答卷导入"]')).toBe(nav);
      const result = readiness();
      result.checks.find((check) => check.code === "student_scope")!.passed = false;
      await act(async () => resolveReadiness({ readiness: result }));
      expect(preparationButtons(view.container)[0].classList.contains("is-incomplete")).toBe(true);
      expect(view.container.querySelector('output')).toBeNull();
    } finally { await view.close(); }
  });

  it("后台核对失败后显示待检查，不把保留的旧绿色当成最新结果", async () => {
    let rejectReadiness!: (error: Error) => void;
    getExamReadiness.mockResolvedValueOnce({ readiness: readiness() });
    getExamReadiness.mockImplementationOnce(() => new Promise((_, reject) => { rejectReadiness = reject; }));
    const view = await render(workspace());
    try {
      await view.update(workspace({ updated_at: "2026-10-12T02:00:00Z" }), "paper");
      expect(preparationButtons(view.container)[0].classList.contains("is-complete")).toBe(true);
      await act(async () => rejectReadiness(new Error("unavailable")));
      expect(preparationButtons(view.container).every((button) => button.classList.contains("is-unknown"))).toBe(true);
      expect(preparationButtons(view.container).every((button) => button.title.includes("暂无法获取"))).toBe(true);
    } finally { await view.close(); }
  });

  it("步骤内容按需加载时仅替换内容区，导航节点和绿色保持不变", async () => {
    let resolveModule!: (value: { default: () => ReactNode }) => void;
    const Module = lazy(() => new Promise<{ default: () => ReactNode }>((resolve) => { resolveModule = resolve; }));
    const view = await render(workspace());
    try {
      const nav = view.container.querySelector('[aria-label="考试准备与答卷导入"]');
      await view.update(workspace(), "template", true, <Module />);
      expect(view.container.textContent).toContain("正在加载答题卡设置");
      expect(view.container.querySelector('[aria-label="考试准备与答卷导入"]')).toBe(nav);
      expect(preparationButtons(view.container)[0].classList.contains("is-complete")).toBe(true);
      await act(async () => resolveModule({ default: () => <p>答题卡内容已加载</p> }));
      expect(view.container.textContent).toContain("答题卡内容已加载");
      expect(view.container.querySelector('[aria-label="考试准备与答卷导入"]')).toBe(nav);
      expect(getExamReadiness).toHaveBeenCalledTimes(1);
    } finally { await view.close(); }
  });

  it("已有完成结果时撤销读取权限立即隐藏状态，后续步骤切换不发送请求", async () => {
    const view = await render(workspace());
    try {
      expect(preparationButtons(view.container)[0].classList.contains("is-complete")).toBe(true);
      await view.update(workspace(), "paper", false);
      await view.update(workspace(), "questions", false);
      expect(preparationButtons(view.container).every((button) => button.classList.contains("is-unknown"))).toBe(true);
      expect(preparationButtons(view.container).every((button) => button.title.includes("无法查看"))).toBe(true);
      expect(getExamReadiness).toHaveBeenCalledTimes(1);
    } finally { await view.close(); }
  });

  it.each([{ read_only: true }, { exam_status: "archived" }])("只读或归档学科不显示写入下一步 $read_only/$exam_status", async (state) => {
    const view = await render(workspace({ ...state, next_actions: [{ code: "prepare", label: "继续配置", description: "配置题目", route: "/exams/math/questions", priority: "normal" }] }));
    try { expect(view.container.querySelector('[aria-label="当前下一步"]')).toBeNull(); }
    finally { await view.close(); }
  });

  it("六步导航保留当前考试编码路径，学生答卷入口不提前执行导入", async () => {
    const view = await render(workspace({ exam_id: "exam /1", stage: "capture" }));
    try {
      expect(view.container.querySelector('[aria-label="切换考试环节"]')).toBeNull();
      const buttons = workflowButtons(view.container);
      expect(buttons).toHaveLength(6);
      for (const [index, path] of ["students", "paper", "questions", "template", "settings", "capture"].entries()) {
        await act(async () => buttons[index].click());
        expect(view.navigate).toHaveBeenLastCalledWith(`/exams/exam%20%2F1/${path}`);
      }
      expect(buttons[5].title).toContain("请先确认准备完成");
      expect(buttons[5].classList.contains("is-complete")).toBe(false);
    } finally { await view.close(); }
  });

  it("切换准备与答卷导入保留导航和同版本检查，识别处理仍选中学生答卷", async () => {
    getExamReadiness.mockResolvedValue({ readiness: readiness({ confirmed: true }) });
    const view = await render(workspace());
    try {
      const original = workflowButtons(view.container);
      for (const section of ["capture", "processing", "template"]) {
        await view.update(workspace(), section);
        const current = workflowButtons(view.container);
        expect(current.every((button, index) => button === original[index])).toBe(true);
        expect(current.filter(button => button.getAttribute("aria-current") === "step")).toHaveLength(1);
        expect(current[section === "template" ? 3 : 5].getAttribute("aria-current")).toBe("step");
        expect(current.slice(0, 5).every(button => button.classList.contains("is-complete"))).toBe(true);
        expect(current[5].classList.contains("is-complete")).toBe(false);
      }
      expect(getExamReadiness).toHaveBeenCalledOnce();
    } finally { await view.close(); }
  });

  it.each(["grading", "quality", "scores", "appeals", "reports"])("%s 保留独立业务页面，不混入考试准备步骤", async (section) => {
    const view = await render(workspace(), section);
    try {
      expect(workflowButtons(view.container)).toHaveLength(0);
      expect(view.container.querySelector('[aria-label="切换考试环节"]')).toBeNull();
      expect(getExamReadiness).not.toHaveBeenCalled();
    } finally { await view.close(); }
  });

  it("删除环节下拉后保留学科切换，切换后仍进入相同学生答卷页面", async () => {
    const view = await render(workspace({ subject_exams: [
      { exam_id: "math", subject: "math", total_score: 150 },
      { exam_id: "english /2", subject: "english", total_score: 150 }
    ] }), "capture");
    try {
      const select = view.container.querySelector<HTMLSelectElement>('[aria-label="切换考试学科"]')!;
      expect(select).not.toBeNull();
      await act(async () => { select.value = "english /2"; select.dispatchEvent(new Event("change", { bubbles: true })); });
      expect(view.navigate).toHaveBeenLastCalledWith("/exams/english%20%2F2/capture");
    } finally { await view.close(); }
  });

});
