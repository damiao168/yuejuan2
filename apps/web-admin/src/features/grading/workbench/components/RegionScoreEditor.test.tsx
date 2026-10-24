// @vitest-environment jsdom
import { act, useState, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewTaskContext } from "../../../../api/review";
import { createInitialDraft } from "../gradingWorkbench.model";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { RegionScoreEditor } from "./RegionScoreEditor";
import { ApiClientError } from "../../../../api/client";

const api = vi.hoisted(() => ({ context: vi.fn(), renew: vi.fn(), save: vi.fn(), submit: vi.fn(), state: vi.fn(), submitted: vi.fn(), saved: vi.fn() }));
vi.mock("../../../../api/review", () => ({ getReviewTaskContext: api.context, renewReviewTask: api.renew, saveReviewDraft: api.save, submitHumanGrade: api.submit }));
vi.mock("antd", () => ({
  Alert: ({ message }: { message: string }) => <p role="alert">{message}</p>,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Input: (props: object) => <input {...props} />,
  Button: ({ children, loading: _loading, type: _type, ...props }: { children: ReactNode; loading?: boolean; type?: string }) => <button {...props}>{children}</button>
}));
vi.mock("../../../../components/MathMarkdown", () => ({ MathMarkdown: ({ children }: { children: ReactNode }) => <span>{children}</span> }));
vi.mock("./FrozenQuestionReference", () => ({ referenceText: (value: unknown) => String(value ?? "") }));
vi.mock("../../SharedScoreControl", () => ({ SharedScoreControl: ({ value, disabled, rubricPoints, onChange }: {
  value: { score: number | null }; disabled: boolean; rubricPoints: { id: string }[];
  onChange: (value: { score: number; rubricSelections: Record<string, number> }) => void;
}) => <div><output aria-label="当前得分">{value.score ?? "未评分"}</output>{[0, 2, 4].map(score => <button key={score} disabled={disabled}
  onClick={() => onChange({ score, rubricSelections: { [rubricPoints[0].id]: score } })}>{score}分</button>)}
  <button disabled={disabled} onClick={() => onChange({ score: 4, rubricSelections: { [rubricPoints[0].id]: 0 } })}>总分调整为4</button></div> }));

function context(no: number): ReviewTaskContext {
  return {
    task: { id: `t${no}`, tenant_id: "tenant", assigned_to: "teacher", submission_id: "student-page", exam_id: "exam", grade_round: "first", status: "in_progress", question_id: `q${no}`, question_no: String(no), revision: no },
    expected_revision: no,
    question: { id: `q${no}`, score: 6 }, question_snapshot: { id: `s${no}` },
    frozen_rubric: { version: `r${no}`, points: [{ id: `p${no}`, description: `第${no}题标准`, score: 6 }] },
    answer_artifact: { grading_group_id: "fill", grading_group_question_ids: ["q12", "q13", "q14"],
      grading_group_tasks: [12, 13, 14].map(i => ({ id: `t${i}`, question_id: `q${i}`, question_no: String(i), status: "in_progress", revision: i })) },
    claim: { can_renew: true }, draft: null
  } as unknown as ReviewTaskContext;
}
function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => { resolve = done; });
  return { promise, resolve };
}

describe("整区共图独立录分", () => {
  let root: Root, host: HTMLDivElement;
  let current = true;
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks(); current = true;
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    api.context.mockImplementation(async (id: string) => ({ context: context(Number(id.slice(1))) }));
    api.renew.mockResolvedValue(undefined);
    api.save.mockImplementation(async (_id: string, body: object) => ({ draft: { ...body, revision: 1 } }));
    api.submit.mockImplementation(async (id: string) => ({ task: { ...context(Number(id.slice(1))).task, status: "submitted", revision: 100 } }));
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.useRealTimers(); });
  async function mount() {
    const value = context(12);
    const work = { task: value.task, reviewContext: value, gradingGroupId: "fill", gradingGroupImageUrl: "/shared", ocrResults: [], ocrText: "" } as unknown as WorkbenchContext;
    function Shell() {
      const [activeWork, setWork] = useState(work);
      const [draft, setDraft] = useState(() => createInitialDraft(work));
      return <RegionScoreEditor context={activeWork} currentUserId="teacher" canGrade draft={draft} setDraft={setDraft}
        onStateChange={api.state} onSubmitted={task => {
          api.submitted(task);
          setWork(previous => previous.task.id === task.id ? { ...previous, task,
            reviewContext: { ...previous.reviewContext, task, expected_revision: task.revision } } : previous);
        }} onDraftSaved={api.saved} isCurrentScope={() => current} />;
    }
    await act(async () => root.render(<Shell />));
  }
  function row(no: number) { return host.querySelector<HTMLElement>(`section[aria-label="第 ${no} 题评分"]`)!; }
  async function click(no: number, text: string) {
    const button = [...row(no).querySelectorAll("button")].find(item => item.textContent === text)!;
    await act(async () => button.click());
  }
  async function settle() { await act(async () => { await vi.advanceTimersByTimeAsync(1200); }); }
  it("三题自动分别提交；一题失败保留其它回执和本题分数", async () => {
    await mount();
    await click(12, "2分"); await click(13, "0分"); await click(14, "4分");
    api.submit.mockImplementation(async (id: string) => { if (id === "t14") throw new Error("network"); return { task: { ...context(Number(id.slice(1))).task, status: "submitted", revision: 100 } }; });
    await settle();
    expect(api.save).toHaveBeenCalledWith("t12", expect.objectContaining({ score: 2, expected_revision: 0, rubric_selections: [{ point_id: "p12", score: 2 }] }));
    expect(api.submit).toHaveBeenCalledWith("t13", expect.objectContaining({ auto_submit: true, score: 0, expected_revision: 13, rubric_selections: [] }));
    expect(row(12).textContent).toContain("已提交");
    expect(row(13).textContent).toContain("已提交");
    expect(row(14).querySelector("output")?.textContent).toBe("4");
    expect(row(14).querySelector('[role="alert"]')).not.toBeNull();
    expect(api.submitted).toHaveBeenCalledTimes(2);
    api.submit.mockImplementation(async (id: string) => ({ task: { ...context(Number(id.slice(1))).task, status: "submitted", revision: 100 } }));
    await click(14, "重试本题提交");
    expect(api.submitted).toHaveBeenCalledTimes(3);
  });

  it("提交当前12题只更新本行，保留13和14的分数与未保存状态", async () => {
    await mount();
    await click(12, "2分"); await click(13, "0分"); await click(14, "4分");
    await settle();
    expect(row(12).textContent).toContain("本题已提交：2 / 6 分");
    expect(row(13).textContent).toContain("本题已提交：0 / 6 分");
    expect(row(14).textContent).toContain("本题已提交：4 / 6 分");
    expect(api.context).toHaveBeenCalledTimes(2);
    expect(api.state).toHaveBeenLastCalledWith(false, false);
    expect(api.save).toHaveBeenCalledWith("t14", expect.objectContaining({ score: 4 }));
  });

  it("同一题连续保存使用上一份成功回执的新草稿版本", async () => {
    api.save.mockImplementation(async (_id: string, body: { expected_revision: number }) => ({ draft: { ...body, revision: body.expected_revision + 1 } }));
    api.submit.mockRejectedValue(new ApiClientError(409, "review_task_marked", "已标记"));
    await mount(); await click(12, "2分"); await settle();
    await click(12, "4分"); await settle();
    expect(api.save.mock.calls.map(([id, body]) => [id, body.score, body.expected_revision])).toEqual([["t12", 2, 0], ["t12", 4, 1]]);
    expect(api.state).toHaveBeenLastCalledWith(false, false);
  });

  it("未填分不自动提交，直接最终分无需隐藏评分点或额外原因", async () => {
    await mount(); await settle();
    expect(api.submit).not.toHaveBeenCalled();
    await click(13, "总分调整为4"); await settle();
    expect(api.submit).toHaveBeenCalledWith("t13", expect.objectContaining({ score: 4, auto_submit: true, rubric_selections: [] }));
  });

  it("历史已提交行缺少正式得分时不展示空草稿为未评分", async () => {
    api.context.mockImplementation(async (id: string) => {
      const value = context(Number(id.slice(1)));
      if (id === "t13") value.task.status = "submitted";
      return { context: value };
    });
    await mount();
    expect(row(13).textContent).toContain("本题已提交，得分请查看提交记录");
    expect(row(13).textContent).not.toContain("未评分");
    expect(row(13).querySelector("output")).toBeNull();
    expect(row(13).querySelector("button")).toBeNull();
  });
  it.each(["student", "reviewer", "round", "exam", "group"])("拒绝混入不同%s的题目上下文", async kind => {
    const wrong = context(13);
    if (kind === "student") wrong.task.submission_id = "other-page";
    if (kind === "reviewer") wrong.task.assigned_to = "other-teacher";
    if (kind === "round") wrong.task.grade_round = "second";
    if (kind === "exam") wrong.task.exam_id = "other-exam";
    if (kind === "group") wrong.answer_artifact.grading_group_id = "other-group";
    api.context.mockResolvedValue({ context: wrong });
    await mount();
    expect(host.textContent).toContain("本区题目加载失败");
    expect(host.querySelector('section[aria-label="第 13 题评分"]')).toBeNull();
    expect(api.submit).not.toHaveBeenCalled();
  });
  it("同一事件重复点击只发一份请求，会话变化后不续发提交", async () => {
    await mount(); await click(13, "2分");
    const waiting = deferred(); api.renew.mockReturnValue(waiting.promise);
    await settle();
    expect(api.renew).toHaveBeenCalledTimes(1);
    current = false;
    await act(async () => waiting.resolve());
    expect(api.submit).not.toHaveBeenCalled();
    expect(api.submitted).not.toHaveBeenCalled();
  });
});
