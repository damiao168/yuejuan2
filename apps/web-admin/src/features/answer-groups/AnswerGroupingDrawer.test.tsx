// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AnswerGroup, AnswerGroupMemberContext, AnswerGroupMetrics, TeacherReferenceCase } from "@edugrade/sdk";
import { AnswerGroupingDrawer } from "./AnswerGroupingDrawer";

const mocks = vi.hoisted(() => ({ questions: vi.fn(), groups: vi.fn(), metrics: vi.fn(), build: vi.fn(), rollback: vi.fn(),
  review: vi.fn(), save: vi.fn(), confirm: vi.fn(), success: vi.fn(), error: vi.fn(), warning: vi.fn(),
  reads: new Map<string, (value: AnswerGroupMemberContext) => void>() }));
vi.mock("../../api/papers", () => ({ listQuestions: mocks.questions }));
vi.mock("../../api/answerGroups", () => ({ listAnswerGroups: mocks.groups, getAnswerGroupMetrics: mocks.metrics,
  buildAnswerGroups: mocks.build, confirmAnswerGroup: mocks.confirm, reviewAnswerGroupSample: mocks.review, rollbackAnswerGroup: mocks.rollback, saveAnswerGroupDecision: mocks.save }));
vi.mock("./AnswerGroupSampleEvidence", () => ({
  AnswerGroupSampleEvidence: ({ groupId, segmentId, onRead }: { groupId: string; segmentId: string; onRead: (value: AnswerGroupMemberContext) => void }) => {
    mocks.reads.set(`${groupId}:${segmentId}`, onRead);
    return <span>实际样本材料入口</span>;
  }
}));
vi.mock("antd", () => {
  const passthrough = ({ children }: { children?: ReactNode }) => <>{children}</>;
  const Item = Object.assign(({ children, className, actions }: { children?: ReactNode; className?: string; actions?: ReactNode[] }) => <div className={className}>{children}{actions}</div>, {
    Meta: ({ title, description }: { title?: ReactNode; description?: ReactNode }) => <div data-testid="list-item-meta"><div>{title}</div><div>{description}</div></div>
  });
  return {
    App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error, warning: mocks.warning } }) },
    Drawer: ({ open, children, extra }: { open: boolean; children: ReactNode; extra?: ReactNode }) => open ? <div>{extra}{children}</div> : null,
    Button: ({ children, onClick, disabled, loading }: { children: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean }) => <button type="button" onClick={onClick} disabled={disabled || loading} aria-busy={loading}>{children}</button>,
    List: Object.assign(({ dataSource, renderItem }: { dataSource: unknown[]; renderItem: (item: unknown) => ReactNode }) => <div>{dataSource.map((item, index) => <div key={index}>{renderItem(item)}</div>)}</div>, { Item }),
    Space: passthrough, Spin: ({ children, spinning }: { children: ReactNode; spinning?: boolean }) => <div data-testid="group-loading" aria-busy={spinning}>{children}</div>, Tag: ({ children, color }: { children: ReactNode; color?: string }) => <span data-color={color}>{children}</span>,
    Alert: ({ message, description }: { message?: ReactNode; description?: ReactNode }) => <div><span>{message}</span><div>{description}</div></div>,
    Select: ({ value, onChange, options, mode }: { value?: string | string[]; onChange?: (value: string | string[]) => void; options?: { value: string; label: string }[]; mode?: string }) => (
      <select aria-label={mode === "multiple" ? "选择采分点" : "选择题目"} multiple={mode === "multiple"} value={value ?? (mode === "multiple" ? [] : "")} onChange={(event) => onChange?.(mode === "multiple" ? Array.from(event.target.selectedOptions, (option) => option.value) : event.target.value)}>
        {options?.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
    ),
    Progress: () => null, Descriptions: Object.assign(passthrough, { Item: ({ label, children }: { label: string; children: ReactNode }) => <span data-testid={`metric-${label}`}>{children}</span> }),
    Input: { TextArea: ({ value, onChange }: { value: string; onChange: (event: React.ChangeEvent<HTMLTextAreaElement>) => void }) => <textarea aria-label="回滚原因" value={value} onChange={onChange} /> },
    InputNumber: ({ value, onChange }: { value: number | null; onChange: (value: number | null) => void }) => <input aria-label="候选分数" type="number" value={value ?? ""} onChange={(event) => onChange(event.target.value === "" ? null : Number(event.target.value))} />,
    Modal: ({ open, title, children, onOk, onCancel, confirmLoading }: { open: boolean; title: string; children: ReactNode; onOk: () => void; onCancel: () => void; confirmLoading: boolean }) => open ? <div role="dialog" aria-label={title}>{children}<button type="button" disabled={confirmLoading} onClick={onOk}>确认回滚</button><button type="button" onClick={onCancel}>取消回滚</button></div> : null,
    Empty: () => null, Divider: () => null,
    Popconfirm: passthrough, Tooltip: passthrough
  };
});

function group(id: string, count: number, questionId = "question"): AnswerGroup {
  return { id, tenant_id: "school", exam_id: "exam", question_id: questionId, exam_question_snapshot_id: "snapshot",
    algorithm_version: "v1", representation_version: "v1", member_count: count, representative_submission_id: id,
    homogeneity: 0.9, status: "sampling", minimum_sample: 2, reviewed_sample_count: 0, can_confirm: false, members: [], created_at: "", updated_at: "" };
}

afterEach(() => { cleanup(); vi.resetAllMocks(); mocks.reads.clear(); });

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((nextResolve, nextReject) => { resolve = nextResolve; reject = nextReject; });
  return { promise, resolve, reject };
}

const questionChoices = [
  { id: "A", question_no: "1", question_type: "short_answer" },
  { id: "B", question_no: "2", question_type: "short_answer" }
];
type GroupResponse = { answer_groups: AnswerGroup[]; teacher_reference_cases: TeacherReferenceCase[] };

function reference(questionId: string): TeacherReferenceCase {
  return { gold_paper_id: questionId, submission_id: questionId, version: 1, reference_score: 1, max_score: 2,
    explanation: `${questionId}题参考案例`, trait_scores: {}, error_tags: [] };
}

function metric(count: number): AnswerGroupMetrics {
  return { group_count: count, member_count: count, group_homogeneity: 0.9, batch_override_rate: 0,
    human_actions_saved: count, post_audit_evidence_status: "pending" };
}

function confirmedGroup(questionId: string): AnswerGroup {
  return { ...group(`${questionId}-group`, questionId === "A" ? 2 : 8, questionId), status: "confirmed",
    decision: { id: `${questionId}-decision`, score_candidate: { score: 1 }, rubric_selection: { rubric_points: [`${questionId}-point`] },
      sample_size: 2, minimum_sample: 2, revision: 1, rollback_reference: `${questionId}-rollback` } };
}

function sampleMaterial(selected: AnswerGroup): AnswerGroupMemberContext {
  return { group_id: selected.id, member: selected.members[0], expected_revision: 1, answer_text: "实际答案", segment_image_url: "",
    question: { id: selected.question_id, exam_id: selected.exam_id, question_no: "1", question_type: "short_answer", score: 2,
      rubric: { id: "rubric", question_id: selected.question_id, version: "1", status: "approved", max_score: 2,
        points: [{ id: `${selected.question_id}-point`, description: `${selected.question_id}题采分点`, score: 2 }] } } };
}

function loadConfirmedGroups() {
  mocks.groups.mockImplementation((_examId: string, nextQuestionId: string) => Promise.resolve({
    answer_groups: [confirmedGroup(nextQuestionId)], teacher_reference_cases: []
  }));
  mocks.metrics.mockResolvedValue({ metrics: undefined });
}
describe("答案分组可访问选择", () => {
  it("通过原生按钮选择另一答案组并同步aria-pressed", async () => {
    mocks.questions.mockResolvedValue({ questions: [{ id: "question", question_no: "1", question_type: "short_answer" }] });
    mocks.groups.mockResolvedValue({ answer_groups: [group("A", 2), group("B", 8)], teacher_reference_cases: [] });
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    render(<AnswerGroupingDrawer open examId="exam" canManage={false} onClose={vi.fn()} />);
    const first = await screen.findByRole("button", { name: /2 份答案/ });
    const second = screen.getByRole("button", { name: /8 份答案/ });
    expect(screen.getByRole("option", { name: "1 · 简答题" })).toBeTruthy();
    expect(document.body.textContent).not.toContain("short_answer");
    expect(first.getAttribute("type")).toBe("button"); expect(second.getAttribute("type")).toBe("button");
    expect(first.getAttribute("aria-pressed")).toBe("true"); expect(second.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(second);
    expect(first.getAttribute("aria-pressed")).toBe("false"); expect(second.getAttribute("aria-pressed")).toBe("true");
  });

  it("按代表、边界、异常排序展示独立角色颜色和抽检状态", async () => {
    const selected = group("samples", 3);
    selected.members = [
      { submission_id: "sample-out", segment_id: "out", similarity: 0.653, outlier_score: 0.923, representative: false, boundary: false, outlier: true, sample_status: "rejected", representation_hash: "out" },
      { submission_id: "sample-bnd", segment_id: "bnd", similarity: 0.875, outlier_score: 0.123, representative: false, boundary: true, outlier: false, sample_status: "accepted", representation_hash: "bnd" },
      { submission_id: "sample-rep", segment_id: "rep", similarity: 0.987, outlier_score: 0.456, representative: true, boundary: true, outlier: true, representation_hash: "rep" }
    ];
    mocks.questions.mockResolvedValue({ questions: [{ id: "question", question_no: "1", question_type: "short_answer" }] });
    mocks.groups.mockResolvedValue({ answer_groups: [selected], teacher_reference_cases: [] });
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    render(<AnswerGroupingDrawer open examId="exam" canManage={false} onClose={vi.fn()} />);
    await screen.findByText("答卷 sample-rep");
    const samples = screen.getAllByTestId("list-item-meta");
    expect(samples.map((sample) => within(sample).getByText(/^答卷 /).textContent)).toEqual(["答卷 sample-rep", "答卷 sample-bnd", "答卷 sample-out"]);
    expect(within(samples[0]).getAllByText(/^(代表样本|边界样本|异常样本)$/).map((tag) => [tag.textContent, tag.getAttribute("data-color")])).toEqual([
      ["代表样本", "blue"], ["边界样本", "orange"], ["异常样本", "red"]
    ]);
    expect(within(samples[1]).getByText("边界样本").getAttribute("data-color")).toBe("orange");
    expect(within(samples[2]).getByText("异常样本").getAttribute("data-color")).toBe("red");
    expect(within(samples[0]).getByText("相似度 98.7% · 异常度 45.6% · 未抽检")).toBeTruthy();
    expect(within(samples[1]).getByText("相似度 87.5% · 异常度 12.3% · 已接受")).toBeTruthy();
    expect(within(samples[2]).getByText("相似度 65.3% · 异常度 92.3% · 已拒绝")).toBeTruthy();
    expect(selected.members.map((member) => member.segment_id)).toEqual(["out", "bnd", "rep"]);
  });

  it.each<{ canManage: boolean; status: AnswerGroup["status"]; canReview: boolean }>([
    { canManage: false, status: "sampling", canReview: false },
    { canManage: true, status: "confirmed", canReview: false },
    { canManage: true, status: "rolled_back", canReview: false },
    { canManage: true, status: "sampling", canReview: true },
    { canManage: true, status: "ready_for_confirmation", canReview: true }
  ])("管理权限=$canManage、状态=$status 时抽检和候选入口可见性一致", async ({ canManage, status, canReview }) => {
    const selected = { ...group("managed", 1), status };
    selected.members = [{ submission_id: "sample", segment_id: "sample", similarity: 0.9, outlier_score: 0.1, representative: true, boundary: false, outlier: false, representation_hash: "sample" }];
    mocks.questions.mockResolvedValue({ questions: [{ id: "question", question_no: "1", question_type: "short_answer" }] });
    mocks.groups.mockResolvedValue({ answer_groups: [selected], teacher_reference_cases: [] });
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    render(<AnswerGroupingDrawer open examId="exam" canManage={canManage} onClose={vi.fn()} />);
    await screen.findByText("答卷 sample");
    for (const name of ["接受", "拒绝", "保存候选", "确认组候选"]) {
      expect(Boolean(screen.queryByRole("button", { name }))).toBe(canReview);
    }
  });

  it("切题后慢请求的旧答案组不会覆盖当前题", async () => {
    const slowA = deferred<{ answer_groups: AnswerGroup[]; teacher_reference_cases: never[] }>();
    mocks.groups.mockImplementation((_examId: string, nextQuestionId: string) => nextQuestionId === "A"
      ? slowA.promise
      : Promise.resolve({ answer_groups: [group("B-group", 8, "B")], teacher_reference_cases: [] }));
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={[
      { id: "A", question_no: "1", question_type: "short_answer" },
      { id: "B", question_no: "2", question_type: "short_answer" }
    ]} canManage={false} onClose={vi.fn()} />);

    const questionSelect = await screen.findByLabelText("选择题目");
    fireEvent.change(questionSelect, { target: { value: "B" } });
    await screen.findByRole("button", { name: /8 份答案/ });

    await act(async () => { slowA.resolve({ answer_groups: [group("A-group", 2, "A")], teacher_reference_cases: [] }); await slowA.promise; });
    await waitFor(() => expect(screen.queryByRole("button", { name: /2 份答案/ })).toBeNull());
    expect(screen.getByRole("button", { name: /8 份答案/ })).toBeTruthy();
  });

  it("切题后旧题的构建操作不会重新加载或选中旧答案组", async () => {
    const buildA = deferred<unknown>();
    mocks.groups.mockImplementation((_examId: string, nextQuestionId: string) => Promise.resolve({
      answer_groups: [group(nextQuestionId === "A" ? "A-group" : "B-group", nextQuestionId === "A" ? 2 : 8, nextQuestionId)],
      teacher_reference_cases: []
    }));
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    mocks.build.mockReturnValue(buildA.promise);
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={[
      { id: "A", question_no: "1", question_type: "short_answer" },
      { id: "B", question_no: "2", question_type: "short_answer" }
    ]} canManage onClose={vi.fn()} />);

    await screen.findByRole("button", { name: /2 份答案/ });
    fireEvent.click(screen.getByRole("button", { name: "构建分组" }));
    expect(mocks.build).toHaveBeenCalledWith("exam", "A");
    const callsForA = mocks.groups.mock.calls.filter((call) => call[1] === "A").length;
    fireEvent.change(screen.getAllByLabelText("选择题目")[0], { target: { value: "B" } });
    await screen.findByRole("button", { name: /8 份答案/ });

    await act(async () => { buildA.resolve({}); await buildA.promise; });
    await waitFor(() => expect(screen.queryByRole("button", { name: /2 份答案/ })).toBeNull());
    expect(mocks.groups.mock.calls.filter((call) => call[1] === "A")).toHaveLength(callsForA);
    expect(screen.getByRole("button", { name: /8 份答案/ })).toBeTruthy();
    expect(mocks.success).not.toHaveBeenCalled();
  });
});

describe("答案分组异步上下文隔离", () => {
  it.each(["success", "failure"])("A题慢加载%s不覆盖B题的答案组、指标和参考案例", async (outcome) => {
    const slowA = deferred<GroupResponse>();
    mocks.groups.mockImplementation((_examId: string, nextQuestionId: string) => nextQuestionId === "A" ? slowA.promise
      : Promise.resolve({ answer_groups: [group("B-group", 8, "B")], teacher_reference_cases: [reference("B")] }));
    mocks.metrics.mockImplementation((_examId: string, nextQuestionId: string) => Promise.resolve({ metrics: metric(nextQuestionId === "A" ? 2 : 8) }));
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage={false} onClose={vi.fn()} />);
    await waitFor(() => expect(mocks.groups).toHaveBeenCalledWith("exam", "A"));
    fireEvent.change(screen.getByLabelText("选择题目"), { target: { value: "B" } });
    await screen.findByRole("button", { name: /8 份答案/ });
    await act(async () => {
      if (outcome === "success") slowA.resolve({ answer_groups: [group("A-group", 2, "A")], teacher_reference_cases: [reference("A")] });
      else slowA.reject(new Error("A题旧请求失败"));
      await slowA.promise.catch(() => undefined);
    });
    expect(screen.queryByRole("button", { name: /2 份答案/ })).toBeNull();
    expect(screen.getByRole("button", { name: /8 份答案/ })).toBeTruthy();
    expect(screen.getByText("B题参考案例")).toBeTruthy();
    expect(screen.queryByText("A题参考案例")).toBeNull();
    expect(screen.queryByText("A题旧请求失败")).toBeNull();
    expect(screen.getByTestId("metric-组数").textContent).toBe("8");
    expect(screen.getByTestId("metric-节省人工动作").textContent).toBe("8");
    expect(screen.getByTestId("group-loading").getAttribute("aria-busy")).toBe("false");
  });

  it("切考试后同题号的旧加载不能覆盖新考试", async () => {
    const oldExam = deferred<GroupResponse>();
    mocks.groups.mockImplementation((examId: string) => examId === "exam" ? oldExam.promise : Promise.resolve({
      answer_groups: [{ ...group("new-exam-group", 8, "A"), exam_id: "exam-new" }], teacher_reference_cases: [reference("新考试")]
    }));
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    const view = render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage={false} onClose={vi.fn()} />);
    await waitFor(() => expect(mocks.groups).toHaveBeenCalledWith("exam", "A"));
    view.rerender(<AnswerGroupingDrawer open examId="exam-new" initialQuestionId="A" availableQuestions={questionChoices} canManage={false} onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /8 份答案/ });
    await act(async () => { oldExam.resolve({ answer_groups: [group("old-exam-group", 2, "A")], teacher_reference_cases: [] }); await oldExam.promise; });
    expect(screen.queryByRole("button", { name: /2 份答案/ })).toBeNull();
    expect(screen.getByText("新考试题参考案例")).toBeTruthy();
  });

  it("旧题构建晚结束不能清除当前题仍在构建的忙碌状态", async () => {
    const buildA = deferred<unknown>();
    const buildB = deferred<unknown>();
    mocks.groups.mockImplementation((_examId: string, nextQuestionId: string) => Promise.resolve({
      answer_groups: [group(`${nextQuestionId}-group`, nextQuestionId === "A" ? 2 : 8, nextQuestionId)], teacher_reference_cases: []
    }));
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    mocks.build.mockImplementation((_examId: string, nextQuestionId: string) => nextQuestionId === "A" ? buildA.promise : buildB.promise);
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /2 份答案/ });
    fireEvent.click(screen.getByRole("button", { name: "构建分组" }));
    fireEvent.change(screen.getByLabelText("选择题目"), { target: { value: "B" } });
    await screen.findByRole("button", { name: /8 份答案/ });
    fireEvent.click(screen.getByRole("button", { name: "构建分组" }));
    expect(mocks.build.mock.calls).toEqual([["exam", "A"], ["exam", "B"]]);
    await act(async () => { buildA.resolve({}); await buildA.promise; });
    expect(screen.getByRole("button", { name: "构建分组" }).getAttribute("aria-busy")).toBe("true");
    expect(mocks.success).not.toHaveBeenCalled();
    await act(async () => { buildB.resolve({}); await buildB.promise; });
    await waitFor(() => expect(screen.getByRole("button", { name: "构建分组" }).getAttribute("aria-busy")).toBe("false"));
    expect(mocks.success).toHaveBeenCalledTimes(1);
  });

  it("关闭再打开同一题后，关闭前的构建不能刷新新面板", async () => {
    const oldBuild = deferred<unknown>();
    mocks.groups.mockResolvedValue({ answer_groups: [group("A-group", 2, "A")], teacher_reference_cases: [] });
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    mocks.build.mockReturnValue(oldBuild.promise);
    const view = render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /2 份答案/ });
    fireEvent.click(screen.getByRole("button", { name: "构建分组" }));
    expect(mocks.build).toHaveBeenCalledTimes(1);
    view.rerender(<AnswerGroupingDrawer open={false} examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    view.rerender(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /2 份答案/ });
    const loadCount = mocks.groups.mock.calls.length;
    await act(async () => { oldBuild.resolve({}); await oldBuild.promise; });
    expect(mocks.groups).toHaveBeenCalledTimes(loadCount);
    expect(mocks.success).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "构建分组" }).getAttribute("aria-busy")).toBe("false");
  });

  it.each(["success", "failure"])("A题回滚晚%s保留B题正在填写的回滚对话框", async (outcome) => {
    const rollbackA = deferred<unknown>();
    loadConfirmedGroups();
    mocks.rollback.mockReturnValue(rollbackA.promise);
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /2 份答案/ });
    fireEvent.click(screen.getByRole("button", { name: "回滚候选" }));
    fireEvent.change(screen.getByLabelText("回滚原因"), { target: { value: "A题原因" } });
    fireEvent.click(screen.getByRole("button", { name: "确认回滚" }));
    expect(mocks.rollback).toHaveBeenCalledWith("A-group", "A-rollback", "A题原因");
    fireEvent.change(screen.getByLabelText("选择题目"), { target: { value: "B" } });
    await screen.findByRole("button", { name: /8 份答案/ });
    fireEvent.click(screen.getByRole("button", { name: "回滚候选" }));
    fireEvent.change(screen.getByLabelText("回滚原因"), { target: { value: "B题原因" } });
    const loadCount = mocks.groups.mock.calls.length;
    await act(async () => {
      if (outcome === "success") rollbackA.resolve({});
      else rollbackA.reject(new Error("A题旧回滚失败"));
      await rollbackA.promise.catch(() => undefined);
    });
    expect(screen.getByRole("dialog", { name: "回滚第2题的 8 份候选" })).toBeTruthy();
    expect((screen.getByLabelText("回滚原因") as HTMLTextAreaElement).value).toBe("B题原因");
    expect(mocks.groups).toHaveBeenCalledTimes(loadCount);
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("当前题回滚失败后保留原因，成功后才关闭对话框", async () => {
    loadConfirmedGroups();
    mocks.rollback.mockRejectedValueOnce(new Error("回滚失败")).mockResolvedValueOnce({});
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /2 份答案/ });
    fireEvent.click(screen.getByRole("button", { name: "回滚候选" }));
    fireEvent.change(screen.getByLabelText("回滚原因"), { target: { value: "保留重试原因" } });
    fireEvent.click(screen.getByRole("button", { name: "确认回滚" }));
    await waitFor(() => expect(mocks.error).toHaveBeenCalledTimes(1));
    expect((screen.getByLabelText("回滚原因") as HTMLTextAreaElement).value).toBe("保留重试原因");
    fireEvent.click(screen.getByRole("button", { name: "确认回滚" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(mocks.rollback).toHaveBeenCalledTimes(2);
    expect(mocks.success).toHaveBeenCalledTimes(1);
  });

  it("同题关闭后重新填写回滚原因，旧回滚完成不能清理新对话框", async () => {
    const oldRollback = deferred<unknown>();
    loadConfirmedGroups();
    mocks.rollback.mockReturnValue(oldRollback.promise);
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /2 份答案/ });
    fireEvent.click(screen.getByRole("button", { name: "回滚候选" }));
    fireEvent.change(screen.getByLabelText("回滚原因"), { target: { value: "首次提交原因" } });
    fireEvent.click(screen.getByRole("button", { name: "确认回滚" }));
    expect(mocks.rollback).toHaveBeenCalledWith("A-group", "A-rollback", "首次提交原因");
    fireEvent.click(screen.getByRole("button", { name: "取消回滚" }));
    fireEvent.click(screen.getByRole("button", { name: "回滚候选" }));
    fireEvent.change(screen.getByLabelText("回滚原因"), { target: { value: "重新填写原因" } });
    await act(async () => { oldRollback.resolve({}); await oldRollback.promise; });
    expect(screen.getByRole("dialog", { name: "回滚第1题的 2 份候选" })).toBeTruthy();
    expect((screen.getByLabelText("回滚原因") as HTMLTextAreaElement).value).toBe("重新填写原因");
  });

  it("关闭抽屉同步关闭独立回滚对话框", async () => {
    loadConfirmedGroups();
    const view = render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /2 份答案/ });
    fireEvent.click(screen.getByRole("button", { name: "回滚候选" }));
    expect(screen.getByRole("dialog")).toBeTruthy();
    view.rerender(<AnswerGroupingDrawer open={false} examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("切题再返回后拒绝旧代次材料，只有新读取能启用抽检", async () => {
    const sampleA = group("A-group", 2, "A");
    sampleA.members = [{ submission_id: "submission-A", segment_id: "segment-A", similarity: 0.9, outlier_score: 0.1,
      representative: true, boundary: false, outlier: false, representation_hash: "hash-A" }];
    mocks.groups.mockImplementation((_examId: string, nextQuestionId: string) => Promise.resolve({
      answer_groups: [nextQuestionId === "A" ? sampleA : group("B-group", 8, "B")], teacher_reference_cases: []
    }));
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    mocks.review.mockResolvedValue({});
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByText("答卷 submission");
    const oldRead = mocks.reads.get("A-group:segment-A")!;
    fireEvent.change(screen.getByLabelText("选择题目"), { target: { value: "B" } });
    await screen.findByRole("button", { name: /8 份答案/ });
    fireEvent.change(screen.getByLabelText("选择题目"), { target: { value: "A" } });
    await screen.findByRole("button", { name: /2 份答案/ });
    // 模拟旧图片显示回调已进入队列；即使题目、组和hash相同，也必须拒绝旧代次登记。
    act(() => oldRead(sampleMaterial(sampleA)));
    expect((screen.getByRole("button", { name: "接受" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.queryByRole("option", { name: "A题采分点 · 2分" })).toBeNull();
    act(() => mocks.reads.get("A-group:segment-A")!(sampleMaterial(sampleA)));
    expect((screen.getByRole("button", { name: "接受" }) as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "接受" }));
    expect(mocks.review).toHaveBeenCalledWith("A-group", "segment-A", "accepted");
    await waitFor(() => expect(mocks.success).toHaveBeenCalledTimes(1));
  });

  it.each(["exam", "question"])("样本返回其他%s材料时不能启用抽检或保存候选", async (wrongScope) => {
    const selected = group("A-group", 2, "A");
    selected.members = [{ submission_id: "submission-A", segment_id: "segment-A", similarity: 0.9, outlier_score: 0.1,
      representative: true, boundary: false, outlier: false, representation_hash: "hash-A" }];
    mocks.groups.mockResolvedValue({ answer_groups: [selected], teacher_reference_cases: [] });
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /2 份答案/ });
    const wrongMaterial = sampleMaterial(selected);
    wrongMaterial.question = { ...wrongMaterial.question, ...(wrongScope === "exam" ? { exam_id: "exam-other" } : { id: "B" }) };
    act(() => mocks.reads.get("A-group:segment-A")!(wrongMaterial));
    expect((screen.getByRole("button", { name: "接受" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "保存候选" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.queryByRole("option", { name: "A题采分点 · 2分" })).toBeNull();
    expect(mocks.review).not.toHaveBeenCalled();
    expect(mocks.save).not.toHaveBeenCalled();
  });

  it("其他题的组对象不显示编辑或回滚提交入口", async () => {
    mocks.groups.mockResolvedValue({ answer_groups: [confirmedGroup("B")], teacher_reference_cases: [] });
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /8 份答案/ });
    expect(screen.queryByRole("button", { name: "回滚候选" })).toBeNull();
    expect(screen.queryByRole("button", { name: "保存候选" })).toBeNull();
  });

  it("缺失题号和未知题型用中文提示，不把UUID放进回滚摘要", async () => {
    const id = "question-uuid-internal";
    mocks.groups.mockResolvedValue({ answer_groups: [confirmedGroup(id)], teacher_reference_cases: [] });
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    render(<AnswerGroupingDrawer open examId="exam" initialQuestionId={id} availableQuestions={[
      { id, question_no: "", question_type: "unknown_type" }
    ]} canManage onClose={vi.fn()} />);
    await screen.findByRole("button", { name: /8 份答案/ });
    expect(screen.getByRole("option", { name: "题号待核对 · 题型待核对" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "回滚候选" }));
    expect(screen.getByRole("dialog", { name: "回滚当前题目的 8 份候选" })).toBeTruthy();
    expect(document.body.textContent).not.toContain(id);
    expect(document.body.textContent).not.toContain("unknown_type");
    expect(document.body.textContent).not.toContain("Rubric");
  });

  it("切考试后题目列表失败不会保留旧请求的加载状态", async () => {
    const slowA = deferred<GroupResponse>();
    mocks.groups.mockReturnValue(slowA.promise);
    mocks.metrics.mockResolvedValue({ metrics: undefined });
    mocks.questions.mockRejectedValue(new Error("新考试题目加载失败"));
    const view = render(<AnswerGroupingDrawer open examId="exam" initialQuestionId="A" availableQuestions={questionChoices} canManage={false} onClose={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("group-loading").getAttribute("aria-busy")).toBe("true"));
    view.rerender(<AnswerGroupingDrawer open examId="exam-new" initialQuestionId="A" canManage={false} onClose={vi.fn()} />);
    await screen.findByText("新考试题目加载失败");
    expect(screen.getByTestId("group-loading").getAttribute("aria-busy")).toBe("false");
    await act(async () => { slowA.resolve({ answer_groups: [group("A-group", 2, "A")], teacher_reference_cases: [] }); await slowA.promise; });
    expect(screen.queryByRole("button", { name: /2 份答案/ })).toBeNull();
  });
});
