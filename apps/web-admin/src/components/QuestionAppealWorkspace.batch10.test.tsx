// @vitest-environment jsdom
import { act, type ChangeEvent, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PublishedQuestionAppeal, PublishedQuestionAppealContext } from "../api/questionAppeals";
import type { ManagedUser } from "../api/users";
import { QuestionAppealWorkspace } from "./QuestionAppealWorkspace";
import { ApiClientError } from "../api/client";

const mocks = vi.hoisted(() => ({
  list: vi.fn(), context: vi.fn(), image: vi.fn(), users: vi.fn(), assign: vi.fn(), decide: vi.fn(), opinion: vi.fn(), resolve: vi.fn(), jobs: vi.fn(), confirm: vi.fn(), changed: vi.fn(),
  createURL: vi.fn(), revokeURL: vi.fn(), controls: new Map<string, Record<string, unknown>>()
}));
const NativeURL = globalThis.URL;
vi.mock("../api/questionAppeals", () => ({ listQuestionAppeals: mocks.list, getQuestionAppealContext: mocks.context, getQuestionAppealAnswerImage: mocks.image, startQuestionAppealReview: mocks.assign, decideQuestionAppeal: mocks.decide, submitQuestionAppealReviewOpinion: mocks.opinion, resolveQuestionAppeal: mocks.resolve }));
vi.mock("../api/users", () => ({ listManagedUsers: mocks.users }));
vi.mock("../api/scoreReleases", () => ({ listRegradeJobs: mocks.jobs }));
vi.mock("../router/useUnsavedChangesGuard", () => ({ useUnsavedChangesGuard: vi.fn() }));
vi.mock("./QuestionAppealRegradeLaunch", () => ({ QuestionAppealRegradeLaunch: () => null }));
vi.mock("antd", async () => {
  const React = await vi.importActual<typeof import("react")>("react");
  const Text = ({ children }: { children: ReactNode }) => React.createElement("span", null, children);
  const List = Object.assign(({ dataSource, renderItem, className }: { dataSource: Array<Record<string, unknown>>; renderItem: (item: Record<string, unknown>) => ReactNode; className?: string }) => React.createElement("div", { className }, dataSource.map((item, index) => React.createElement("div", { key: String(item.id ?? item.key ?? index), "data-item-key": String(item.key ?? item.id ?? index) }, renderItem(item)))), {
    Item: ({ children, actions, className }: { children: ReactNode; actions?: ReactNode[]; className?: string }) => React.createElement("article", { className }, children, actions)
  });
  const Empty = Object.assign(({ description }: { description: string }) => React.createElement("div", null, description), { PRESENTED_IMAGE_SIMPLE: "simple" });
  return {
    List, Empty, Typography: { Text, Paragraph: Text }, App: { useApp: () => ({ modal: { confirm: mocks.confirm } }) },
    Alert: ({ message, description, action }: { message: string; description?: string; action?: ReactNode }) => React.createElement("div", { "data-alert": message }, message, description, action),
    Button: (props: Record<string, unknown>) => { mocks.controls.set(String(props.children), props); return React.createElement("button", { disabled: Boolean(props.disabled), onClick: props.onClick as () => void }, props.children as ReactNode); },
    Drawer: (props: Record<string, unknown>) => { mocks.controls.set("drawer", props); return React.createElement("div", null, props.children as ReactNode); },
    Select: (props: Record<string, unknown>) => {
      mocks.controls.set(props.className === "question-appeal-regrade-id" ? "job" : props["aria-label"] === "选择复核结果成绩版本" ? "result" : props.id === "question-appeal-review-opinion" ? "opinion" : "reviewer", props);
      return React.createElement("select", { className: props.className as string, value: String(props.value ?? ""), onChange: (event: ChangeEvent<HTMLSelectElement>) => (props.onChange as (value: string) => void)(event.target.value) },
        React.createElement("option", { value: "" }, props.placeholder as string),
        (props.options as Array<{ value: string; label: string }>).map((option) => React.createElement("option", { key: option.value, value: option.value }, option.label)));
    },
    InputNumber: (props: Record<string, unknown>) => { mocks.controls.set("suggested-score", props); return React.createElement("input", { id: props.id as string, value: String(props.value ?? ""), onChange: (event: ChangeEvent<HTMLInputElement>) => (props.onChange as (value: number | null) => void)(event.target.value ? Number(event.target.value) : null) }); },
    Input: { TextArea: (props: Record<string, unknown>) => { mocks.controls.set(props.id === "question-appeal-review-explanation" ? "explanation" : "reply", props); return React.createElement("textarea", { id: props.id as string, value: String(props.value), onChange: props.onChange as (event: ChangeEvent<HTMLTextAreaElement>) => void }); } },
    Space: ({ children }: { children: ReactNode }) => React.createElement("div", null, children),
    Tag: ({ children, color }: { children: ReactNode; color?: string }) => React.createElement("span", { "data-color": color }, children)
  };
});

function appeal(id: string, overrides: Partial<PublishedQuestionAppeal> = {}): PublishedQuestionAppeal {
  return { id, tenant_id: "tenant", exam_id: "exam", submission_id: `submission-${id}`, source_release_id: "release", source_release_version: 2, question_id: `question-${id}`, question_no: id, source_score: 0, source_max_score: 10, reason_code: "rubric_disagreement", reason: `${id} 学生说明`, status: "under_review", assigned_to: "teacher", revision: 3, created_at: "2026-10-04", updated_at: "2026-10-04", ...overrides };
}
function context(id: string, overrides: Partial<PublishedQuestionAppealContext> = {}): PublishedQuestionAppealContext {
  return { appeal: appeal(id), source_type: "human", rubric_snapshot: { points: [{ id: 0, code: "代码", description: "说明", score: 0, points: 2 }, { name: "  名称  ", points: 2 }] }, release_history: [{ id: "release", version: 2, source: "initial", status: "published", reason: "首次发布" }], answer_image_ready: false, ...overrides };
}
function user(id: string, roles: string[], status = "active"): ManagedUser { return { id, username: id, display_name: `${id} 姓名`, status, roles }; }
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: Error) => void; const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; }); return { promise, resolve, reject }; }

describe("题目申诉共同旧新回归：模拟 API/控件", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    vi.resetAllMocks(); mocks.controls.clear();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("URL", Object.assign(class extends NativeURL {}, { createObjectURL: mocks.createURL, revokeObjectURL: mocks.revokeURL }));
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.list.mockResolvedValue({ appeals: [appeal("A"), appeal("B")] });
    mocks.context.mockImplementation(async (id: string) => ({ context: context(id) }));
    mocks.users.mockResolvedValue({ users: [user("teacher", ["teacher"])] });
    mocks.assign.mockResolvedValue({ appeal: appeal("A") }); mocks.decide.mockResolvedValue({ appeal: appeal("A") });
    mocks.resolve.mockResolvedValue({ appeal: appeal("A", { status: "resolved" }) });
    mocks.jobs.mockResolvedValue({ regrade_jobs: [{ id: "governed-job", question_id: "question-A", source_release_id: "release", reason_text: "本题人工复评", affected_count: 1 }, { id: "other-job", question_id: "question-B", source_release_id: "release", reason_text: "其他题", affected_count: 1 }] });
    mocks.confirm.mockImplementation((options: { onOk: () => void }) => options.onOk());
    mocks.createURL.mockReturnValue("blob:answer-A");
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.unstubAllGlobals(); });
  async function render(props: Partial<Parameters<typeof QuestionAppealWorkspace>[0]> = {}) { await act(async () => root.render(<QuestionAppealWorkspace examId="exam" canManage canWork canManageScores onChanged={mocks.changed} {...props} />)); }
  function control(label: string) { const value = mocks.controls.get(label); expect(value, label).toBeDefined(); return value!; }
  function click(label: string) { return (control(label).onClick as () => unknown)(); }
  function select(id: string) { const row = [...host.querySelectorAll(".question-appeal-list article")].find((node) => node.querySelector("strong")?.textContent?.startsWith(`${id} ·`)); expect(row).toBeDefined(); row!.querySelector<HTMLButtonElement>("button")!.click(); }
  function job(value: string) { (control("job").onChange as (value: string) => void)(value); }
  function reply(value: string) { (control("reply").onChange as (event: { target: { value: string } }) => void)({ target: { value } }); }

  it("没有管理或工作权限时不显示也不取列表", async () => {
    await render({ canManage: false, canWork: false });
    expect(host.textContent).toBe(""); expect(mocks.list).not.toHaveBeenCalled(); expect(mocks.users).not.toHaveBeenCalled();
  });

  it("受派教师只提交独立意见，不显示管理员答复分派或关联复评", async () => {
    await render({ canManage: false, canManageScores: false, currentUserId: "teacher" });
    expect(mocks.list).toHaveBeenCalledWith("exam"); expect(mocks.users).not.toHaveBeenCalled();
    expect(host.textContent).toContain("保存意见并移交学校管理员"); expect(host.textContent).not.toContain("答复并维持原成绩"); expect(host.textContent).not.toContain("关联题目级复评"); expect(host.querySelector(".question-appeal-reviewer")).toBeNull(); expect(mocks.jobs).not.toHaveBeenCalled();
  });

  it("仅管理权限仍显示复核和复评入口，Drawer 保持原尺寸", async () => {
    await render({ canWork: false });
    expect(mocks.list).toHaveBeenCalledWith("exam"); expect(mocks.users).toHaveBeenCalledWith({ limit: 100 });
    expect(host.textContent).toContain("答复并维持原成绩"); expect(host.textContent).toContain("关联题目级复评"); expect(control("drawer").width).toBe(760); expect(control("drawer").open).toBe(true);
  });

  it.each(["support_adjustment", "do_not_support", "needs_evidence"] as const)("受派教师将 %s 意见和零分建议移交，仍等待管理员且不调用裁决或成绩接口", async (opinion) => {
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { revision: 7 }) }) });
    const saved = { opinion, suggested_score: 0, explanation: "核对原卷后提出独立意见", submitted_at: "2026-10-09T03:00:00Z" };
    mocks.opinion.mockResolvedValue({ appeal: appeal("A", { revision: 8, review_opinion: saved }) });
    await render({ canManage: false, canManageScores: false, currentUserId: "teacher", personalScope: true });
    await act(async () => {
      (control("opinion").onChange as (value: string) => void)(opinion);
      (control("suggested-score").onChange as (value: number) => void)(0);
      (control("explanation").onChange as (event: { target: { value: string } }) => void)({ target: { value: "  核对原卷后提出独立意见  " } });
    });
    await act(async () => click("保存意见并移交学校管理员"));
    expect(mocks.opinion).toHaveBeenCalledWith("A", { opinion, suggested_score: 0, explanation: saved.explanation, expected_revision: 7 });
    expect(mocks.decide).not.toHaveBeenCalled(); expect(mocks.resolve).not.toHaveBeenCalled(); expect(mocks.jobs).not.toHaveBeenCalled();
    expect(host.textContent).toContain("待学校管理员处理意见"); expect(host.textContent).toContain("成绩尚未改变");
    expect(host.textContent).toContain("建议本题得分：0 / 10"); expect(mocks.changed).toHaveBeenCalledTimes(1);
  });

  it("意见提交同步防重复，修订冲突保留意见分值说明且不自动刷新重发", async () => {
    const pending = deferred<{ appeal: PublishedQuestionAppeal }>(); mocks.opinion.mockReturnValue(pending.promise);
    await render({ canManage: false, canManageScores: false, currentUserId: "teacher" });
    await act(async () => {
      (control("opinion").onChange as (value: string) => void)("support_adjustment");
      (control("suggested-score").onChange as (value: number) => void)(7);
      (control("explanation").onChange as (event: { target: { value: string } }) => void)({ target: { value: "第二步应补过程分" } });
    });
    await act(async () => { click("保存意见并移交学校管理员"); click("保存意见并移交学校管理员"); select("B"); });
    expect(mocks.opinion).toHaveBeenCalledTimes(1); expect(control("drawer").title).toContain("第 A 题复核材料");
    await act(async () => pending.reject(new Error("resource_version_conflict")));
    expect(control("opinion").value).toBe("support_adjustment"); expect(control("suggested-score").value).toBe(7); expect(control("explanation").value).toBe("第二步应补过程分");
    expect(mocks.context).toHaveBeenCalledTimes(1); expect(mocks.changed).not.toHaveBeenCalled();
    expect(control("保存意见并移交学校管理员").loading).toBe(false);
  });

  it.each(["", "unassigned-worker"])("账号 %s 未被当前指派时没有意见提交入口", async (currentUserId) => {
    await render({ canManage: false, canManageScores: false, currentUserId });
    expect(host.textContent).not.toContain("保存意见并移交学校管理员"); expect(host.textContent).not.toContain("答复并维持原成绩");
  });

  it.each([
    ["appeal_source_release_invalid", "学校已更新当前公布成绩"],
    ["resource_version_conflict", "本申请已被其他人更新"]
  ])("%s 拒绝意见时保留填写内容并指出当前核对对象", async (code, message) => {
    mocks.opinion.mockRejectedValue(new ApiClientError(409, code, "internal detail"));
    await render({ canManage: false, canManageScores: false, currentUserId: "teacher" });
    await act(async () => {
      (control("opinion").onChange as (value: string) => void)("needs_evidence");
      (control("explanation").onChange as (event: { target: { value: string } }) => void)({ target: { value: "请补充原卷第二页" } });
    });
    await act(async () => click("保存意见并移交学校管理员"));
    expect(host.textContent).toContain(message); expect(host.textContent).toContain("填写内容已保留");
    expect(control("explanation").value).toBe("请补充原卷第二页"); expect(control("opinion").value).toBe("needs_evidence");
    expect(mocks.context).toHaveBeenCalledTimes(1); expect(mocks.decide).not.toHaveBeenCalled(); expect(mocks.opinion.mock.calls[0][1]).not.toHaveProperty("suggested_score");
  });

  it("管理员读取已移交的私有建议，学校队列区分等待教师与等待管理员", async () => {
    const review_opinion = { opinion: "needs_evidence" as const, explanation: "请补充原始扫描证据", submitted_at: "2026-10-09T03:00:00Z" };
    mocks.list.mockResolvedValue({ appeals: [appeal("A", { review_opinion }), appeal("B")] });
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { review_opinion }) }) });
    await render();
    expect(host.textContent).toContain("请补充原始扫描证据"); expect(host.textContent).toContain("待学校管理员处理意见"); expect(host.textContent).toContain("待独立教师意见");
    expect(host.textContent).toContain("答复并维持原成绩"); expect(host.textContent).toContain("关联题目级复评");
    expect(host.textContent).not.toContain("保存意见并移交学校管理员");
  });

  it.each([
    { subject: "  语文 / 历史  ", expected: "语文 / 历史" },
    { subject: "  english  ", expected: "英语" },
    { subject: "mathematics", expected: "数学" },
    { subject: "unknown", expected: "其他学科" },
    { subject: "  ", expected: "" },
    { subject: undefined, expected: "" },
  ])("队列和材料摘要保持学科分支：$subject", async ({ subject, expected }) => {
    const task = appeal("A", { exam_name: "  高一期中  ", exam_subject: subject, question_no: "3" });
    mocks.list.mockResolvedValue({ appeals: [task] });
    mocks.context.mockResolvedValue({ context: context("A", { appeal: task }) });
    await render({ canManage: false, canManageScores: false, personalScope: true, currentUserId: "teacher" });
    const summary = ["高一期中", expected, "第 3 题"].filter(Boolean).join(" · ");
    expect(host.querySelector(".question-appeal-list")?.textContent).toContain(summary);
    expect(host.querySelector(".question-appeal-context")?.textContent).toContain(summary);
    expect(mocks.users).not.toHaveBeenCalled();
    expect(mocks.jobs).not.toHaveBeenCalled();
  });

  it.each([
    { status: "submitted", label: "待处理", color: "orange" },
    { status: "rejected", label: "已答复", color: "blue" },
    { status: "upheld_pending_regrade", label: "已转复评", color: "blue" },
    { status: "resolved", label: "已完成", color: "green" },
    { status: "unknown", label: "处理中", color: "blue" },
  ])("非意见等待状态 $status 保持队列名称与颜色", async ({ status, label, color }) => {
    const task = appeal("A", { status });
    mocks.list.mockResolvedValue({ appeals: [task] });
    mocks.context.mockResolvedValue({ context: context("A", { appeal: task }) });
    await render();
    const tag = host.querySelector(".question-appeal-list [data-color]");
    expect(tag?.textContent).toBe(label);
    expect(tag?.getAttribute("data-color")).toBe(color);
  });

  it.each(["rejected", "resolved", "unknown"])("%s 状态保持只读，不显示处理控件", async (status) => {
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { status }) }) });
    await render();
    expect(host.querySelector(".question-appeal-actions")).toBeNull(); expect(host.querySelector(".question-appeal-facts")).not.toBeNull();
  });

  it("工作端不能分派待处理申诉，空队列显示原空状态", async () => {
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { status: "submitted" }) }) });
    await render({ canManage: false }); expect(host.querySelector(".question-appeal-actions")).toBeNull();
    mocks.list.mockResolvedValueOnce({ appeals: [] });
    await act(async () => { click("刷新"); });
    expect(host.textContent).toContain("当前考试没有题目申诉"); expect(host.querySelector(".question-appeal-list")).toBeNull(); expect(control("drawer").open).toBe(false);
  });

  it("管理端只列活跃指定角色，分派仍使用队列 revision", async () => {
    mocks.users.mockResolvedValue({ users: [user("teacher", ["teacher"]), user("grader", ["grader"]), user("arbitrator", ["arbitrator"]), user("admin", ["admin"]), user("inactive", ["teacher"], "disabled")] });
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { status: "submitted", revision: 9 }) }) });
    await render(); expect(mocks.users).toHaveBeenCalledWith({ limit: 100 });
    expect(control("reviewer").options).toEqual(["teacher", "grader", "arbitrator"].map((id) => ({ value: id, label: `${id} 姓名` })));
    await act(async () => { (control("reviewer").onChange as (value: string) => void)("grader"); });
    await act(async () => { expect(click("开始复核")).toBeUndefined(); });
    expect(mocks.assign).toHaveBeenCalledWith("A", { assigned_to: "grader", expected_revision: 3 }); expect(mocks.changed).toHaveBeenCalledTimes(1);
  });

  it("分派选择重渲染保持 DOM 与焦点，未选择人员时保持禁用", async () => {
    mocks.users.mockResolvedValue({ users: [user("teacher", ["teacher"]), user("grader", ["grader"])] });
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { status: "submitted", assigned_to: "" }) }) });
    await render(); const selectElement = host.querySelector<HTMLSelectElement>(".question-appeal-reviewer")!; selectElement.focus();
    expect(control("开始复核").disabled).toBe(true);
    await act(async () => { (control("reviewer").onChange as (value: string) => void)("grader"); });
    expect(host.querySelector(".question-appeal-reviewer")).toBe(selectElement); expect(document.activeElement).toBe(selectElement); expect(control("开始复核").disabled).toBe(false);
  });

  it("刷新保留现有选择，移除该项后选首项，关闭只清选择", async () => {
    await render(); await act(async () => select("B"));
    await act(async () => { expect(click("刷新")).toBeUndefined(); });
    expect(control("drawer").title).toContain("第 B 题复核材料");
    mocks.list.mockResolvedValueOnce({ appeals: [appeal("A")] });
    await act(async () => { click("刷新"); }); expect(control("drawer").title).toContain("第 A 题复核材料");
    await act(async () => { (control("drawer").onClose as () => void)(); });
    expect(control("drawer").open).toBe(false); expect(host.querySelector(".question-appeal-context")).toBeNull();
  });

  it("B 已选中后 A 迟到详情不能改事实或分派选择", async () => {
    // B 先完成详情，再放行 A；保护 effect 清理后旧结果不回写当前事实和选择。
    const late = deferred<{ context: PublishedQuestionAppealContext }>();
    mocks.context.mockImplementation((id: string) => id === "A" ? late.promise : Promise.resolve({ context: context("B", { appeal: appeal("B", { status: "submitted", assigned_to: "grader" }) }) }));
    await render(); await act(async () => select("B"));
    await act(async () => late.resolve({ context: context("A", { appeal: appeal("A", { status: "submitted", assigned_to: "old" }) }) }));
    expect(host.querySelector(".question-appeal-facts")?.textContent).toContain("B 学生说明"); expect(control("reviewer").value).toBe("grader");
  });

  it("旧详情失败不覆盖 B；当前详情失败可通过重新选择重试", async () => {
    const late = deferred<{ context: PublishedQuestionAppealContext }>();
    mocks.context.mockReturnValueOnce(late.promise).mockRejectedValueOnce(new Error("detail failed"));
    await render(); await act(async () => select("B"));
    expect(host.querySelector('[data-alert="无法处理此申诉"]')).not.toBeNull();
    await act(async () => late.reject(new Error("old detail failed")));
    await act(async () => select("A"));
    expect(host.querySelector('[data-alert="无法处理此申诉"]')).toBeNull(); expect(host.querySelector(".question-appeal-facts")?.textContent).toContain("A 学生说明");
  });

  it("列表失败结束 loading，刷新重试沿用考试范围", async () => {
    mocks.list.mockRejectedValueOnce(new Error("list failed"));
    await render(); expect(control("刷新").loading).toBe(false); expect(host.querySelector('[data-alert="题目申诉暂时无法加载"]')).not.toBeNull();
    await act(async () => { click("刷新"); });
    expect(mocks.list).toHaveBeenLastCalledWith("exam"); expect(host.querySelector('[data-alert="题目申诉暂时无法加载"]')).toBeNull(); expect(host.querySelector(".question-appeal-facts")).not.toBeNull();
  });

  it("答复用详情 revision与教师填写的公开回复，不附复评编号", async () => {
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { revision: 7 }) }) });
    await render(); await act(async () => { reply("已核对第二步过程，按评分要点维持原成绩。"); });
    await act(async () => { expect(click("答复并维持原成绩")).toBeUndefined(); });
    expect(mocks.decide).toHaveBeenCalledWith("A", { decision: "reject", public_response: "已核对第二步过程，按评分要点维持原成绩。", expected_revision: 7 });
    expect(mocks.changed).toHaveBeenCalledTimes(1); expect(host.textContent).toContain("已答复学生，原成绩保持不变。");
  });

  it("未填写回复不能直接调用处理；没有成绩管理权限不读取或关联复评", async () => {
    await render({ canManageScores: false });
    await act(async () => { click("答复并维持原成绩"); });
    expect(mocks.decide).not.toHaveBeenCalled(); expect(mocks.jobs).not.toHaveBeenCalled();
    expect(host.textContent).toContain("请填写给学生的复核回复"); expect(host.textContent).not.toContain("关联题目级复评");
  });

  it("教师视角只显示分派给当前账号的申请，即使后端返回双角色全校列表", async () => {
    mocks.list.mockResolvedValue({ appeals: [appeal("A", { assigned_to: "teacher", student_id: "private-student-identity" }), appeal("B", { assigned_to: "other" })] });
    await render({ canManage: false, canManageScores: false, personalScope: true, currentUserId: "teacher" });
    expect(host.querySelectorAll(".question-appeal-list article")).toHaveLength(1);
    expect(host.textContent).toContain("A 学生说明"); expect(mocks.users).not.toHaveBeenCalled();
    expect(host.textContent).not.toContain("private-student-identity");
  });

  it("受派教师无需人员或考试目录管理权限，队列详情和保存回执共用任务名称学科题号", async () => {
    const task = appeal("A", { exam_id: "11111111-2222-3333-4444-555555555555", exam_name: "高一第一次月考", exam_subject: "english", student_id: "private-student", question_no: "3" });
    mocks.list.mockResolvedValue({ appeals: [task] }); mocks.context.mockResolvedValue({ context: context("A", { appeal: task }) });
    mocks.opinion.mockResolvedValue({ appeal: { ...task, review_opinion: { opinion: "needs_evidence", explanation: "请核对第二步证据", submitted_at: "" }, revision: 4 } });
    await render({ canManage: false, canManageScores: false, canReadIdentities: false, currentUserId: "teacher", personalScope: true });
    expect(host.querySelector(".question-appeal-list")!.textContent).toContain("高一第一次月考 · 英语 · 第 3 题");
    expect(host.querySelector(".question-appeal-context")!.textContent).toContain("高一第一次月考 · 英语 · 第 3 题");
    expect(host.textContent).not.toContain(task.exam_id); expect(host.textContent).not.toContain("private-student");
    expect(host.querySelector(".question-appeal-list")!.textContent).not.toContain("申请 A");
    expect(mocks.users).not.toHaveBeenCalled(); expect(mocks.jobs).not.toHaveBeenCalled();
    await act(async () => {
      (control("opinion").onChange as (value: string) => void)("needs_evidence");
      (control("explanation").onChange as (event: { target: { value: string } }) => void)({ target: { value: "请核对第二步证据" } });
    });
    await act(async () => click("保存意见并移交学校管理员"));
    expect(host.textContent).toContain("高一第一次月考 · 英语 · 第 3 题：复核意见已保存并移交学校管理员");
  });

  it("考试摘要失败显示同页恢复，重读只更新摘要并保留草稿和原核对版本", async () => {
    const task = appeal("A", { exam_id: "11111111-2222-3333-4444-555555555555" });
    mocks.list.mockResolvedValue({ appeals: [task] }); mocks.context.mockResolvedValue({ context: context("A", { appeal: task }) });
    await render({ canManage: false, canManageScores: false, currentUserId: "teacher", personalScope: true });
    expect(host.textContent).toContain("考试名称暂未读取"); expect(host.textContent).not.toContain(task.exam_id);
    await act(async () => {
      (control("opinion").onChange as (value: string) => void)("needs_evidence");
      (control("suggested-score").onChange as (value: number) => void)(7);
      (control("explanation").onChange as (event: { target: { value: string } }) => void)({ target: { value: "请补充原卷第二页" } });
    });
    mocks.context.mockRejectedValueOnce(new Error("exam summary unavailable"));
    await act(async () => click("重新读取本任务摘要"));
    expect(host.textContent).toContain("考试名称暂未读取"); expect(control("explanation").value).toBe("请补充原卷第二页");
    // 名称恢复时服务端已出现新版分数和修订，摘要重读不能替代教师原先核对的事实或促成新版自动重发。
    mocks.context.mockResolvedValueOnce({ context: context("A", { appeal: { ...task, exam_name: "高一第一次月考", exam_subject: "english", source_score: 9, revision: 999 } }) });
    await act(async () => click("重新读取本任务摘要"));
    expect(host.textContent).toContain("高一第一次月考 · 英语 · 第 A 题");
    expect(control("explanation").value).toBe("请补充原卷第二页"); expect(control("suggested-score").value).toBe(7);
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.opinion).not.toHaveBeenCalled();
    mocks.opinion.mockResolvedValue({ appeal: { ...task, exam_name: "高一第一次月考", exam_subject: "english" } });
    await act(async () => click("保存意见并移交学校管理员"));
    expect(mocks.opinion).toHaveBeenCalledWith("A", { opinion: "needs_evidence", suggested_score: 7, explanation: "请补充原卷第二页", expected_revision: 3 });
  });

  it("切换申请后迟到的摘要不会改变当前任务", async () => {
    await render({ canManage: false, canManageScores: false, currentUserId: "teacher", personalScope: true });
    const late = deferred<{ context: PublishedQuestionAppealContext }>(); mocks.context.mockReturnValueOnce(late.promise);
    await act(async () => click("重新读取本任务摘要"));
    await act(async () => select("B"));
    await act(async () => late.resolve({ context: context("A", { appeal: appeal("A", { exam_name: "上一任务考试", exam_subject: "english" }) }) }));
    expect(host.querySelector(".question-appeal-context")!.textContent).not.toContain("上一任务考试");
    expect(control("drawer").title).toContain("第 B 题复核材料");
  });

  it("分派人员读取完整分页；人员接口失败不把真实申请队列隐藏或误报列表失败", async () => {
    mocks.users.mockResolvedValueOnce({ users: [user("first", ["teacher"])], has_more: true, next_cursor: "next" }).mockResolvedValueOnce({ users: [user("second", ["grader"])], has_more: false });
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { status: "submitted", assigned_to: "" }) }) });
    await render();
    expect(mocks.users).toHaveBeenCalledWith({ limit: 100, cursor: "next" });
    expect(control("reviewer").options).toEqual([{ value: "first", label: "first 姓名" }, { value: "second", label: "second 姓名" }]);
    mocks.users.mockRejectedValueOnce(new Error("没有人员管理权限"));
    await act(async () => click("刷新"));
    expect(host.querySelectorAll(".question-appeal-list article")).toHaveLength(2);
    expect(host.textContent).toContain("可分派教师暂时无法读取");
    expect(host.querySelector('[data-alert="题目申诉暂时无法加载"]')).toBeNull();
  });

  it("转入重评后可以选择真实后续已发布版本结案，失败保留输入和revision", async () => {
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { status: "upheld_pending_regrade", revision: 11, public_response: "正在重新评分" }), release_history: [{ id: "release", version: 2, source: "initial", status: "published", reason: "原版" }, { id: "next", version: 3, source: "regrade", status: "published", reason: "复评完成" }, { id: "draft", version: 4, source: "regrade", status: "draft", reason: "未发布" }] }) });
    await render();
    expect(control("result").options).toEqual([{ value: "next", label: "第 3 版 · 复评完成" }]);
    await act(async () => { (control("result").onChange as (value: string) => void)("next"); reply("复核后本题得分已更正，请查看新版成绩。"); });
    mocks.resolve.mockRejectedValueOnce(new Error("resource_version_conflict"));
    await act(async () => click("公布复核结果"));
    expect(control("reply").value).toContain("复核后本题得分已更正"); expect(control("result").value).toBe("next");
    await act(async () => click("公布复核结果"));
    expect(mocks.resolve).toHaveBeenLastCalledWith("A", { new_release_id: "next", public_response: "复核后本题得分已更正，请查看新版成绩。", expected_revision: 11 });
    expect(host.textContent).toContain("结果已公布，学生可以在我的复核申请中查看");
  });

  it("关联复评只允许真实同题同版本任务，操作失败保留回复与选择", async () => {
    await render(); await act(async () => { reply("已受理，将按本题人工复评流程处理。"); job(""); });
    await act(async () => { expect(click("关联题目级复评")).toBeUndefined(); });
    expect(mocks.decide).not.toHaveBeenCalled(); expect(host.textContent).toContain("请先创建或选择同题同发布版本的复评任务");
    expect(control("job").options).toEqual([{ value: "governed-job", label: "本题人工复评 · 1 份答卷" }]);
    await act(async () => { job("governed-job"); });
    const pending = deferred<{ appeal: PublishedQuestionAppeal }>(); mocks.decide.mockReturnValueOnce(pending.promise);
    await act(async () => { click("关联题目级复评"); }); expect(control("关联题目级复评").loading).toBe(true);
    await act(async () => pending.reject(new Error("decision failed")));
    expect(control("关联题目级复评").loading).toBe(false); expect(control("job").value).toBe("governed-job"); expect(control("reply").value).toBe("已受理，将按本题人工复评流程处理。"); expect(mocks.changed).not.toHaveBeenCalled();
    await act(async () => { click("关联题目级复评"); });
    expect(mocks.decide).toHaveBeenLastCalledWith("A", { decision: "refer_regrade", public_response: "已受理，将按本题人工复评流程处理。", regrade_job_id: "governed-job", expected_revision: 3 }); expect(mocks.changed).toHaveBeenCalledTimes(1);
  });

  it("分派失败保留选项，成功后等待列表及用户刷新再通知", async () => {
    mocks.context.mockResolvedValue({ context: context("A", { appeal: appeal("A", { status: "submitted" }) }) });
    await render(); mocks.assign.mockRejectedValueOnce(new Error("assign failed"));
    await act(async () => { click("开始复核"); }); expect(control("开始复核").loading).toBe(false); expect(control("reviewer").value).toBe("teacher"); expect(mocks.changed).not.toHaveBeenCalled();
    const pendingUsers = deferred<{ users: ManagedUser[] }>(); mocks.users.mockReturnValueOnce(pendingUsers.promise);
    await act(async () => { click("开始复核"); });
    expect(mocks.list).toHaveBeenCalledTimes(2); expect(mocks.changed).not.toHaveBeenCalled(); expect(control("开始复核").loading).toBe(true);
    await act(async () => pendingUsers.resolve({ users: [user("teacher", ["teacher"])] }));
    expect(mocks.changed).toHaveBeenCalledTimes(1); expect(control("开始复核").loading).toBe(false);
  });

  it("处理结论等待刷新后通知，通知时详情重读尚可未完成", async () => {
    await render();
    await act(async () => reply("已核对原卷，维持原成绩。"));
    const pendingList = deferred<{ appeals: PublishedQuestionAppeal[] }>();
    const pendingDetail = deferred<{ context: PublishedQuestionAppealContext }>();
    mocks.list.mockReturnValueOnce(pendingList.promise); mocks.context.mockReturnValueOnce(pendingDetail.promise);
    await act(async () => { expect(click("答复并维持原成绩")).toBeUndefined(); });
    expect(mocks.changed).not.toHaveBeenCalled(); expect(control("答复并维持原成绩").loading).toBe(true); expect(mocks.context).toHaveBeenCalledTimes(1);
    await act(async () => pendingList.resolve({ appeals: [appeal("A"), appeal("B")] }));
    expect(mocks.changed).toHaveBeenCalledTimes(1); expect(mocks.context).toHaveBeenCalledTimes(2); expect(host.querySelector(".question-appeal-context")).toBeNull();
    await act(async () => pendingDetail.resolve({ context: context("A") }));
    expect(control("答复并维持原成绩").loading).toBe(false); expect(host.textContent).toContain("已答复学生，原成绩保持不变。");
  });

  it("输入重渲染保持 DOM 与焦点，切换申请先确认再清理上条回复", async () => {
    await render(); const input = host.querySelector<HTMLTextAreaElement>("textarea")!; input.focus();
    await act(async () => { reply("A题复核草稿"); });
    expect(host.querySelector("textarea")).toBe(input); expect(document.activeElement).toBe(input);
    await act(async () => select("B")); expect(mocks.confirm).toHaveBeenCalledTimes(1); expect(control("reply").value).toBe(""); expect(control("job").value).toBeUndefined();
  });

  it("图像和归一化框保留原样，切换后撤销已经创建的 URL", async () => {
    mocks.context.mockImplementation(async (id: string) => ({ context: context(id, { answer_image_ready: id === "A", appeal: appeal(id, { selected_region: { coordinate_space: "canonical_image_normalized", x: 0.1, y: 0.2, width: 0.3, height: 0.4 } }) }) }));
    mocks.image.mockResolvedValue({ blob: new Blob(["mock answer"]) });
    await render(); expect(mocks.image).toHaveBeenCalledWith("A");
    const image = host.querySelector<HTMLImageElement>(".question-appeal-image img")!; expect(image.getAttribute("src")).toBe("blob:answer-A"); expect(image.alt).toBe("A 答题区域");
    const region = host.querySelector<HTMLElement>(".question-appeal-region")!; expect([region.style.left, region.style.top, region.style.width, region.style.height]).toEqual(["10%", "20%", "30%", "40%"]);
    await act(async () => select("B")); expect(mocks.revokeURL).toHaveBeenCalledWith("blob:answer-A"); expect(host.querySelector(".question-appeal-image img")).toBeNull(); expect(host.textContent).toContain("答题图暂不可用");
  });

  it.each([
    { coordinate_space: "source_image_pixels", x: 1, y: 2, width: 3, height: 4 },
    { coordinate_space: "canonical_image_normalized", x: "0.1", y: 0.2, width: 0.3, height: 0.4 }
  ])("非标准坐标系或非 number 坐标不渲染区域框", async (region) => {
    mocks.context.mockResolvedValue({ context: context("A", { answer_image_ready: true, appeal: appeal("A", { selected_region: region }) }) });
    mocks.image.mockResolvedValue({ blob: new Blob(["mock answer"]) });
    await render(); expect(host.querySelector(".question-appeal-image img")).not.toBeNull(); expect(host.querySelector(".question-appeal-region")).toBeNull();
  });

  it("图像请求失败不影响冻结事实，仍显示原图像不可用提示", async () => {
    mocks.context.mockResolvedValue({ context: context("A", { answer_image_ready: true }) }); mocks.image.mockRejectedValue(new Error("image failed"));
    await render(); expect(host.querySelector(".question-appeal-facts")?.textContent).toContain("A 学生说明"); expect(host.querySelector(".question-appeal-image img")).toBeNull(); expect(host.textContent).toContain("答题图暂不可用");
  });

  it("合法 rubric 标量键、零分与原标签空白保持，历史回退仍可见", async () => {
    mocks.context.mockResolvedValue({ context: context("A", { rubric_snapshot: { points: [null, "bad", { id: 0, code: "代码", score: 0, points: 9 }, { id: false, name: "  名称  ", points: 2 }, {}] }, release_history: [{ id: "old", version: 1, source: "regrade", status: "superseded", reason: "重评" }, { id: "unknown", version: 3, source: "unknown", status: "unknown", reason: "未知" }] }) });
    await render(); expect(host.querySelector('[data-item-key="0"]')?.textContent).toContain("代码0 分"); expect(host.querySelector('[data-item-key="false"]')?.textContent).toContain("  名称  2 分");
    expect(host.textContent).toContain("评分点 5"); expect(host.textContent).toContain("重评发布已被替代"); expect(host.textContent).toContain("其他来源未知状态");
  });
});
