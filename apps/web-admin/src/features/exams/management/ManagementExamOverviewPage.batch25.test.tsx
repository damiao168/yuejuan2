// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam, ManagementExam, ManagementExamKind } from "../../../api/exams";
import { ManagementExamOverviewPage } from "./ManagementExamOverviewPage";

interface DetailsProps {
  exam: ManagementExam;
  gradeName?: string;
  classNames?: Map<string, string>;
  onOpenSubject: (subject: Exam) => void;
}

interface ButtonProps {
  children?: ReactNode;
  onClick?: () => void;
  loading?: boolean;
}

const mocks = vi.hoisted(() => ({ get: vi.fn(), schools: vi.fn(), grades: vi.fn(), classes: vi.fn(), details: vi.fn(), buttons: vi.fn() }));
vi.mock("../../../api/exams", () => ({ getManagementExam: mocks.get }));
vi.mock("../../../api/org", () => ({ listSchools: mocks.schools, listGrades: mocks.grades, listClasses: mocks.classes }));
vi.mock("../../../api/client", () => ({ getUserErrorMessage: (error: Error, fallback: string) => error.message || fallback }));
vi.mock("./ManagementExamDetails", () => ({
  ManagementExamDetails: (props: DetailsProps) => {
    mocks.details(props);
    return <div data-details>{props.exam.name}<button onClick={() => props.onOpenSubject(props.exam.exams[0])}>进入学科</button></div>;
  }
}));
vi.mock("../../../components/PageState", () => ({
  LoadingState: ({ label }: { label: string }) => <div data-loading>{label}</div>,
  ErrorState: ({ message, onRetry }: { message: string; onRetry: () => void }) => <div role="alert">{message}<button onClick={onRetry}>重试</button></div>
}));
vi.mock("antd", () => ({
  Alert: ({ message, description }: { message: ReactNode; description?: ReactNode }) => <div>{message}{description}</div>,
  Space: ({ children }: { children?: ReactNode }) => <>{children}</>,
  Button: (props: ButtonProps) => {
    mocks.buttons(props);
    return <button disabled={props.loading} onClick={props.onClick}>{props.children}</button>;
  }
}));

function exam(id = "session-25", kind: ManagementExamKind = "session"): ManagementExam {
  return {
    id, kind, school_id: "school-25", grade_id: "grade-25", name: `考试 ${id}`, exam_type: "midterm_exam", revision: 3,
    created_by: "admin-25", class_ids: ["class-25"], default_class_ids: ["class-25"], inherited_subject_ids: ["math/25"],
    exams: [{ id: "math/25", tenant_id: "tenant-25", school_id: "school-25", name: "数学", subject: "mathematics", exam_type: "midterm_exam",
      total_score: 150, status: "draft", grading_mode: "ai_assisted", appeal_enabled: true, publish_policy: "manual_publish",
      created_by: "admin-25", class_ids: ["class-25"], revision: 2 }],
    progress: { status: "draft", label: "草稿", total_count: 1, ready_count: 0, published_count: 0, counts: { draft: 1 } },
    scope_complete: true, can_manage: true, can_edit: true, can_archive: true, can_restore: false
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

let container: HTMLDivElement;
let root: Root;
beforeEach(() => {
  vi.resetAllMocks();
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  mocks.get.mockResolvedValue({ exam: exam() });
  mocks.schools.mockResolvedValue({ schools: [] });
  mocks.grades.mockResolvedValue({ grades: [] });
  mocks.classes.mockResolvedValue({ classes: [] });
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
});
afterEach(async () => { await act(async () => root.unmount()); container.remove(); });

async function mount(target = exam(), navigate = vi.fn()) {
  await act(async () => root.render(<ManagementExamOverviewPage kind={target.kind} examId={target.id} onNavigate={navigate} />));
  return navigate;
}

async function click(text: string) {
  const button = [...container.querySelectorAll("button")].find((item) => item.textContent === text);
  if (!button) throw new Error(`Missing button ${text}`);
  await act(async () => button.click());
}

describe("整场考试总览展示边界", () => {
  it("考试与组织名称读取期间保留页头，加载内容优先于已返回的考试", async () => {
    const pendingExam = deferred<{ exam: ManagementExam }>();
    const pendingGrades = deferred<{ grades: [] }>();
    mocks.get.mockReturnValue(pendingExam.promise);
    mocks.grades.mockReturnValue(pendingGrades.promise);
    const target = exam();
    await mount(target);
    expect(container.querySelector("h1")?.textContent).toBe("考试工作区");
    expect(container.textContent).toContain("考试列表");
    expect(container.textContent).toContain("刷新");
    expect(container.querySelector("[data-loading]")).not.toBeNull();
    expect(mocks.details).not.toHaveBeenCalled();

    await act(async () => pendingExam.resolve({ exam: target }));
    expect(container.querySelector("h1")?.textContent).toBe(target.name);
    expect(container.querySelector("[data-loading]")).not.toBeNull();
    expect(mocks.details).not.toHaveBeenCalled();
    await act(async () => pendingGrades.resolve({ grades: [] }));
    expect(container.querySelector("[data-loading]")).toBeNull();
    expect(mocks.details.mock.lastCall?.[0].exam).toBe(target);
  });

  it("考试读取失败保留错误和页头，重试沿用当前整场类型与 ID", async () => {
    const target = exam();
    mocks.get.mockRejectedValueOnce(new Error("考试访问失败")).mockResolvedValueOnce({ exam: target });
    await mount(target);
    expect(container.querySelector("[role='alert']")?.textContent).toContain("考试访问失败");
    expect(container.querySelector("h1")?.textContent).toBe("考试工作区");
    expect(container.querySelector("[data-loading]")).toBeNull();
    expect(mocks.details).not.toHaveBeenCalled();
    expect(mocks.schools).not.toHaveBeenCalled();
    await click("重试");
    expect(mocks.get.mock.calls).toEqual([[target.kind, target.id], [target.kind, target.id]]);
    expect(container.querySelector("[role='alert']")).toBeNull();
    expect(mocks.details.mock.lastCall?.[0].exam).toBe(target);
  });

  it("组织目录部分失败仍展示原整场对象，并保留成功名称和学科导航", async () => {
    const target = exam();
    mocks.get.mockResolvedValue({ exam: target });
    mocks.schools.mockResolvedValue({ schools: [{ id: target.school_id, name: "测试学校" }] });
    mocks.grades.mockRejectedValue(new Error("无年级目录权限"));
    mocks.classes.mockResolvedValue({ classes: [{ id: "class-25", name: "一班" }] });
    const navigate = await mount(target);
    const props = mocks.details.mock.lastCall?.[0] as DetailsProps;
    expect(props.exam).toBe(target);
    expect(mocks.schools).not.toHaveBeenCalled();
    expect(props.gradeName).toBeUndefined();
    expect(props.classNames).toEqual(new Map([["class-25", "一班"]]));
    expect(mocks.grades).toHaveBeenCalledWith(target.school_id);
    expect(container.querySelector("[role='alert']")).toBeNull();
    await click("进入学科");
    await click("考试列表");
    expect(navigate.mock.calls).toEqual([["/exams/math%2F25/students"], ["/exams"]]);
  });

  it.each([
    ["另一 ID", "session-new", "session"],
    ["另一实体类型", "session-25", "standalone"]
  ] as const)("切换到%s 后迟到的考试响应不能覆盖当前详情", async (_, id, kind) => {
    const pendingExam = deferred<{ exam: ManagementExam }>();
    const oldExam = exam();
    const currentExam = exam(id, kind);
    mocks.get.mockReturnValueOnce(pendingExam.promise).mockResolvedValueOnce({ exam: currentExam });
    const navigate = await mount(oldExam);
    await mount(currentExam, navigate);
    expect(mocks.details.mock.lastCall?.[0].exam).toBe(currentExam);
    await act(async () => pendingExam.resolve({ exam: oldExam }));
    expect(mocks.get.mock.calls).toEqual([[oldExam.kind, oldExam.id], [currentExam.kind, currentExam.id]]);
    expect(mocks.grades).toHaveBeenCalledTimes(1);
    expect(container.querySelector("h1")?.textContent).toBe(currentExam.name);
    expect(mocks.details.mock.lastCall?.[0].exam).toBe(currentExam);
  });

  it("同一考试刷新后迟到的组织目录不能覆盖新版考试名称", async () => {
    const pendingGrades = deferred<{ grades: { id: string; name: string }[] }>();
    const target = exam();
    const latest = { ...target, name: "刷新后的考试", revision: 4 };
    mocks.get.mockResolvedValueOnce({ exam: target }).mockResolvedValueOnce({ exam: latest });
    mocks.grades.mockReturnValueOnce(pendingGrades.promise).mockResolvedValueOnce({ grades: [{ id: target.grade_id, name: "当前年级" }] });
    await mount(target);
    // 直接触发页头回调模拟上一轮目录查询尚未结束的刷新，只验证页面淘汰迟到结果的边界。
    const refresh = [...mocks.buttons.mock.calls].reverse().find(([props]) => props.children === "刷新")?.[0].onClick;
    if (!refresh) throw new Error("Missing refresh callback");
    await act(async () => refresh());
    expect(mocks.details.mock.lastCall?.[0].exam).toBe(latest);
    await act(async () => pendingGrades.resolve({ grades: [{ id: target.grade_id, name: "旧年级" }] }));
    const props = mocks.details.mock.lastCall?.[0] as DetailsProps;
    expect(mocks.get.mock.calls).toEqual([[target.kind, target.id], [target.kind, target.id]]);
    expect(container.querySelector("h1")?.textContent).toBe(latest.name);
    expect(props.exam).toBe(latest);
    expect(props.gradeName).toBe("当前年级");
  });
});
