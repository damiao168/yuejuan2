// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Appeal } from "../api/appeals";
import type { SessionUser } from "../auth/session";
import { AppealCenterPage } from "./AppealCenterPage";
import type { SchoolClass, Student } from "../api/org";
import { AppealCaseHeading, AppealCasePanel } from "../features/appeals/AppealEvidence";
import { AppealTaskList } from "../features/appeals/AppealTaskList";
import { assignmentProgress, evidenceNumber, formatScore, readableReason, rubricItems } from "../features/appeals/appealPresentation";

const mocks = vi.hoisted(() => ({
  listAppeals: vi.fn(), getAppeal: vi.fn(), submitAppealRecommendation: vi.fn(),
  assignAppeal: vi.fn(), getAppealStatistics: vi.fn(), listManagedUsers: vi.fn(), confirm: vi.fn(),
  success: vi.fn(), error: vi.fn()
}));

vi.mock("../api/appeals", () => ({
  listAppeals: mocks.listAppeals,
  getAppeal: mocks.getAppeal,
  submitAppealRecommendation: mocks.submitAppealRecommendation,
  assignAppeal: mocks.assignAppeal,
  getAppealStatistics: mocks.getAppealStatistics
}));
vi.mock("../api/users", () => ({ listManagedUsers: mocks.listManagedUsers }));
vi.mock("../components/PageState", async () => {
  const React = await import("react");
  return {
    LoadingState: ({ label }: { label: string }) => React.createElement("div", null, label),
    EmptyState: () => React.createElement("div"),
    ErrorState: () => React.createElement("div")
  };
});
vi.mock("../components/ResponsiveTable", async () => {
  const React = await import("react");
  return {
    ResponsiveTable: ({ dataSource, onRow }: { dataSource: Appeal[]; onRow: (appeal: Appeal) => { onClick: () => void } }) =>
      React.createElement("div", null, dataSource.map((appeal) => React.createElement("button", {
        key: appeal.id, "data-row-id": appeal.id, onClick: onRow(appeal).onClick
      }, appeal.id)))
  };
});
vi.mock("antd", async () => {
  const React = await import("react");
  const Input = Object.assign(
    ({ value, onChange, placeholder }: { value?: string; onChange?: (event: React.ChangeEvent<HTMLInputElement>) => void; placeholder?: string }) =>
      React.createElement("input", { value, onChange, placeholder }),
    { TextArea: ({ value, onChange, placeholder, disabled }: { value?: string; onChange?: (event: React.ChangeEvent<HTMLTextAreaElement>) => void; placeholder?: string; disabled?: boolean }) =>
      React.createElement("textarea", { value, onChange, placeholder, disabled }) }
  );
  return {
    App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error }, modal: { confirm: mocks.confirm } }) },
    Alert: () => null,
    Button: ({ children, onClick, disabled }: { children: React.ReactNode; onClick?: () => void; disabled?: boolean }) =>
      React.createElement("button", { onClick, disabled }, children),
    Input,
    InputNumber: () => null,
    Select: ({ value, onChange, options, disabled }: { value?: string; onChange?: (value: string) => void; options: { value: string; label: string }[]; disabled?: boolean }) =>
      React.createElement("select", { value, disabled, onChange: (event: React.ChangeEvent<HTMLSelectElement>) => onChange?.(event.target.value) }, options.map((option) => React.createElement("option", { key: option.value, value: option.value }, option.label))),
    Space: ({ children }: { children: React.ReactNode }) => React.createElement("div", null, children),
    Tag: ({ children }: { children: React.ReactNode }) => React.createElement("span", null, children)
  };
});

function appeal(id: string): Appeal {
  return {
    id, exam_name: `考试 ${id}`, question_no: id, reason: `申诉 ${id}`,
    status: "under_review", assigned_to: "teacher", revision: 1,
    student_id: "student", exam_id: "exam", subject: "math", anonymous_code: id
  } as Appeal;
}

describe("申诉纯展示边界", () => {
  it("教师标题只展示匿名编号，管理员标题保留学生、班级和提交时间", () => {
    const props = { appeal: appeal("anonymous-A"), student: { name: "真实学生" } as Student, schoolClass: { name: "真实班级" } as SchoolClass };
    const teacher = renderToStaticMarkup(<AppealCaseHeading {...props} mode="teacher" />);
    expect(teacher).toContain("匿名答卷 anonymous-A");
    expect(teacher).not.toContain("真实学生");
    expect(teacher).not.toContain("真实班级");
    const admin = renderToStaticMarkup(<AppealCaseHeading {...props} mode="admin" />);
    expect(admin).toContain("真实学生 · 真实班级");
  });

  it("任务列表采用原生列表，教师条目不泄露已加载的学生姓名", () => {
    const item = appeal("anonymous-A");
    const props = { loading: false, appeals: [item], selectedId: item.id, exams: {}, students: { student: { name: "真实学生" } as Student }, onSelect: () => undefined };
    const teacher = renderToStaticMarkup(<AppealTaskList {...props} mode="teacher" />);
    expect(teacher).toContain("<ul");
    expect(teacher).toContain("<li");
    expect(teacher).toContain('aria-current="true"');
    expect(teacher).toContain("anonymous-A");
    expect(teacher).not.toContain("真实学生");
    expect(renderToStaticMarkup(<AppealTaskList {...props} mode="admin" />)).toContain("真实学生");
    expect(assignmentProgress({ ...item, assigned_to: "" })).toBe("待分派");
    expect(assignmentProgress(item)).toBe("待教师反馈");
    expect(assignmentProgress({ ...item, teacher_recommendation: "reject" })).toBe("已提交复核意见");
  });

  it("加载和未选中状态不会渲染申诉操作内容", () => {
    expect(renderToStaticMarkup(<AppealCasePanel loading hasSelection><button>旧任务操作</button></AppealCasePanel>)).not.toContain("旧任务操作");
    expect(renderToStaticMarkup(<AppealCasePanel loading={false} hasSelection={false}><button>旧任务操作</button></AppealCasePanel>)).not.toContain("旧任务操作");
    expect(renderToStaticMarkup(<AppealCasePanel loading={false} hasSelection><button>当前任务操作</button></AppealCasePanel>)).toContain("当前任务操作");
  });

  it("评分要点保持字段优先级，跳过非法点并保留无分值点", () => {
    const item = { ...appeal("A"), evidence: { rubric: { points: [null, "bad", { description: " ", name: "有效名称", score: 0, points: 3 }, { points: 2 }, {}] }, final_grade: { score: 0, max_score: Infinity } } };
    expect(rubricItems(item)).toEqual([{ label: "有效名称", score: 0 }, { label: "评分点 4", score: 2 }, { label: "评分点 5", score: undefined }]);
    expect(evidenceNumber(item, "score")).toBe(0);
    expect(evidenceNumber(item, "max_score")).toBeUndefined();
    expect(formatScore(NaN)).toBe("-");
    expect(formatScore(2.505)).toBe("2.5");
    expect(readableReason("grading does not match the published rubric")).toBe("评分标准适用有异议");
    expect(readableReason(" ")).toBe("其他明确评分问题");
  });
});

describe("appeal detail selection", () => {
  let root: Root;
  let host: HTMLDivElement;
  let finishA: (value: { appeal: Appeal }) => void;

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    mocks.listAppeals.mockResolvedValue({ appeals: [appeal("A"), appeal("B")] });
    mocks.getAppeal.mockImplementation((id: string) => id === "A"
      ? new Promise((resolve) => { finishA = resolve; })
      : Promise.resolve({ appeal: appeal("B") }));
    mocks.submitAppealRecommendation.mockResolvedValue({});
    mocks.assignAppeal.mockResolvedValue({});
    mocks.getAppealStatistics.mockResolvedValue({ statistics: { total: 2, by_status: {} } });
    mocks.listManagedUsers.mockResolvedValue({ users: [{ id: "other", username: "other", status: "active", roles: ["teacher"] }] });
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    host.remove();
  });

  it("keeps B selected and submits B when A's detail arrives late", async () => {
    await act(async () => {
      root.render(<AppealCenterPage mode="teacher" canRead canManage={false} canWork
        canReadIdentities={false} canReadExams={false}
        currentUser={{ id: "teacher" } as SessionUser} />);
    });
    expect(mocks.getAppeal).toHaveBeenCalledWith("A");
    const taskB = [...host.querySelectorAll<HTMLButtonElement>(".appeal-task-item")].find((button) => button.textContent?.includes("考试 B"));
    expect(taskB).toBeDefined();
    await act(async () => { taskB?.click(); });
    expect(host.textContent).toContain("考试 B");
    await act(async () => { finishA({ appeal: appeal("A") }); });
    expect(host.textContent).toContain("考试 B");
    expect(host.textContent).not.toContain("正在读取答卷与复核信息");

    const reason = host.querySelector<HTMLTextAreaElement>("textarea");
    expect(reason).not.toBeNull();
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(reason, "依据评分标准重新核对答卷后建议维持结果");
      reason?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const submit = [...host.querySelectorAll("button")].find((button) => button.textContent === "提交复核意见");
    await act(async () => { submit?.click(); });
    expect(mocks.submitAppealRecommendation).toHaveBeenCalledWith("B", expect.objectContaining({ expected_revision: 1 }));
  });

  it("keeps the selected appeal and unsaved reason while its submission is pending", async () => {
    let finishSubmission!: () => void;
    mocks.getAppeal.mockImplementation((id: string) => Promise.resolve({ appeal: appeal(id) }));
    mocks.submitAppealRecommendation.mockImplementation(() => new Promise<void>((resolve) => { finishSubmission = resolve; }));
    await act(async () => {
      root.render(<AppealCenterPage mode="teacher" canRead canManage={false} canWork canReadIdentities={false} canReadExams={false} currentUser={{ id: "teacher" } as SessionUser} />);
    });
    const reason = host.querySelector<HTMLTextAreaElement>("textarea");
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(reason, "依据评分标准重新核对答卷后建议维持结果");
      reason?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const submit = [...host.querySelectorAll("button")].find((button) => button.textContent === "提交复核意见");
    await act(async () => { submit?.click(); });
    const taskB = [...host.querySelectorAll<HTMLButtonElement>(".appeal-task-item")].find((button) => button.textContent?.includes("考试 B"));
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.disabled).toBe(true);
    await act(async () => { taskB?.click(); });
    expect(host.querySelector(".appeal-task-item.active")?.textContent).toContain("考试 A");
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toContain("依据评分标准");
    expect(mocks.getAppeal).not.toHaveBeenCalledWith("B");
    await act(async () => { finishSubmission(); });
  });

  it("requires an explicit discard before reassignment clears the administrator's decision", async () => {
    mocks.getAppeal.mockImplementation((id: string) => Promise.resolve({ appeal: appeal(id) }));
    await act(async () => {
      root.render(<AppealCenterPage mode="admin" canRead canManage canWork={false} canReadIdentities={false} canReadExams={false} currentUser={{ id: "admin" } as SessionUser} />);
    });
    const reason = host.querySelector<HTMLTextAreaElement>("textarea");
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(reason, "按评分标准复核后，当前最终分应维持");
      reason?.dispatchEvent(new Event("input", { bubbles: true }));
    });
    const workerSelect = host.querySelector<HTMLSelectElement>(".appeal-reassign select");
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set?.call(workerSelect, "other");
      workerSelect?.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const reassign = [...host.querySelectorAll("button")].find((button) => button.textContent === "确认更换");
    expect(reassign).toBeDefined();
    await act(async () => { reassign?.click(); });
    expect(mocks.assignAppeal).not.toHaveBeenCalled();
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toContain("当前最终分应维持");
    expect(mocks.confirm).toHaveBeenCalledWith(expect.objectContaining({ title: "重新分派并放弃未提交的结论？" }));
    await act(async () => { await mocks.confirm.mock.calls[0][0].onOk(); });
    expect(mocks.assignAppeal).toHaveBeenCalledWith("A", "other", 1);
    expect(host.querySelector<HTMLTextAreaElement>("textarea")?.value).toBe("");
  });

  it.each([true, false])("老筛选迟到=%s时保留当前申诉筛选、选择和详情", async late => {
    let releaseOld!: (value: { appeals: Appeal[] }) => void;
    const initial = appeal("A");
    const filtered = { ...appeal("C"), status: "need_more_info" };
    mocks.getAppeal.mockImplementation(async (id: string) => ({ appeal: id === "C" ? filtered : initial }));
    mocks.listAppeals.mockImplementation(async (query: { status?: string }) => {
      if (query.status === "need_more_info") return { appeals: [filtered] };
      if (late) return await new Promise<{ appeals: Appeal[] }>(resolve => { releaseOld = resolve; });
      return { appeals: [initial] };
    });
    await act(async () => root.render(<AppealCenterPage mode="teacher" canRead canManage={false} canWork canReadIdentities={false} canReadExams={false} currentUser={{ id: "teacher" } as SessionUser} />));
    const status = host.querySelectorAll<HTMLSelectElement>("select")[1];
    await act(async () => { status.value = "need_more_info"; status.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(host.querySelector(".appeal-task-item.active")?.textContent).toContain("考试 C");
    if (late) await act(async () => releaseOld({ appeals: [initial] }));
    expect(status.value).toBe("need_more_info");
    expect(host.querySelector(".appeal-task-item.active")?.textContent).toContain("考试 C");
    expect(mocks.listAppeals.mock.calls.map(call => call[0].status)).toEqual([undefined, "need_more_info"]);
  });
});
