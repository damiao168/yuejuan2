// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../../../api/exams";
import type { RegradeJob, ScoreRelease } from "../../../api/scoreReleases";
import { defaultReleaseVisibility, useScoreReleaseWorkflow } from "./useScoreReleaseWorkflow";

const mocks = vi.hoisted(() => ({ create: vi.fn(), createRegrade: vi.fn(), publish: vi.fn(), error: vi.fn(), confirm: vi.fn(), run: vi.fn(), stepUp: vi.fn(), reload: vi.fn(), preview: vi.fn(),
  Cancelled: class StepUpCancelledError extends Error {}, session: { scope: "t:u", generation: 1 } as { scope: string; generation: number } }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { error: mocks.error }, modal: { confirm: mocks.confirm } }) } }));
vi.mock("../../../api/scoreReleases", () => ({ createScoreRelease: mocks.create, createRegradeScoreRelease: mocks.createRegrade, publishScoreRelease: mocks.publish, revokeHighScorePaper: vi.fn() }));
vi.mock("../../../api/businessCommand", () => ({ getBusinessCommandSession: () => mocks.session, isBusinessCommandSessionCurrent: (value: unknown) => value === mocks.session }));
vi.mock("../../../auth/stepUpContext", () => ({ useStepUp: () => ({ runWithStepUp: mocks.stepUp }), isStepUpCancelledError: (error: unknown) => error instanceof mocks.Cancelled }));

describe("复核申请与成绩发布依赖", () => {
  let root: Root; let host: HTMLDivElement; let workflow: ReturnType<typeof useScoreReleaseWorkflow>;
  const exam = { id: "exam", name: "本次考试", appeal_enabled: true } as unknown as Exam;
  function Harness() {
    workflow = useScoreReleaseWorkflow({ selectedExamId: "exam", selectedExam: exam, educationStage: "junior", runAction: mocks.run, reload: mocks.reload, onPreview: mocks.preview });
    return null;
  }
  beforeEach(async () => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.run.mockImplementation(async (_key: string, action: () => Promise<void>) => action());
    mocks.stepUp.mockImplementation(async ({ action }: { action: () => Promise<void> }) => action());
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.create.mockResolvedValue({ score_release: { id: "new-draft", version: 4 } });
    await act(async () => root.render(<Harness />));
    await act(async () => workflow.openPublish());
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("开放复核却关闭逐题得分时阻止创建，不发送矛盾策略", async () => {
    await act(async () => { workflow.setVisibility({ ...defaultReleaseVisibility, show_question_scores: false }); });
    await act(async () => workflow.submitPublish());
    expect(mocks.create).not.toHaveBeenCalled(); expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("必须公开逐题得分"));
  });

  it("逐题得分公开时允许创建，默认空白名单沿用服务端允许全部规则", async () => {
    await act(async () => workflow.submitPublish());
    expect(mocks.create).toHaveBeenCalledWith("exam", expect.objectContaining({ reason: "首次发布本场成绩", visibility_policy: expect.objectContaining({ show_question_scores: true, show_answers: true }), appeal_window: { enabled: true } }));
  });

  it("旧表单的隐藏答案值不能再次提交，预览和正式发布复用同一份规范化草稿", async () => {
    await act(async () => workflow.setVisibility({ ...defaultReleaseVisibility, show_answers: false, show_exact_rank: false, show_percentile: false }));
    await act(async () => workflow.previewPublish());
    expect(mocks.create).toHaveBeenLastCalledWith("exam", expect.objectContaining({ visibility_policy: expect.objectContaining({ show_answers: true }) }));
    await act(async () => workflow.submitPublish());
    expect(mocks.create).toHaveBeenCalledTimes(1);
    expect(mocks.publish).toHaveBeenCalledWith("new-draft");
  });

  it.each([null, 0, 101, 1.5])("优秀答卷范围%s不是有效整数时，预览与发布都不创建草稿", async topN => {
    await act(async () => {
      workflow.setVisibility({ ...defaultReleaseVisibility, show_high_score_paper: true, show_exact_rank: false, show_percentile: false });
      workflow.setHighScoreTopN(topN);
    });
    await act(async () => { workflow.previewPublish(); workflow.submitPublish(); });
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("1 至 100"));
  });

  it("优秀答卷范围进入冻结草稿标识，调整名额后不能复用先前的学生预览", async () => {
    expect(workflow.highScoreTopN).toBe(10);
    await act(async () => workflow.setVisibility({ ...defaultReleaseVisibility, show_high_score_paper: true, show_exact_rank: false, show_percentile: false }));
    await act(async () => workflow.previewPublish());
    await act(async () => workflow.previewPublish());
    expect(mocks.create).toHaveBeenCalledTimes(1);
    expect(mocks.create).toHaveBeenLastCalledWith("exam", expect.objectContaining({ visibility_policy: expect.objectContaining({ high_score_paper_top_n: 10 }) }));
    const firstKey = mocks.create.mock.calls[0][1].idempotency_key;
    await act(async () => workflow.setHighScoreTopN(3));
    await act(async () => workflow.previewPublish());
    expect(mocks.create).toHaveBeenCalledTimes(2);
    expect(mocks.create).toHaveBeenLastCalledWith("exam", expect.objectContaining({ visibility_policy: expect.objectContaining({ high_score_paper_top_n: 3 }) }));
    expect(mocks.create.mock.calls[1][1].idempotency_key).not.toBe(firstKey);
  });

  it("正式发布校验已冻结草稿，不能用当前表单公开策略掩盖历史矛盾配置", () => {
    workflow.publishRelease({ id: "old-draft", version: 2, appeal_window: { enabled: true }, visibility_policy: { show_question_scores: false } } as unknown as ScoreRelease);
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.publish).not.toHaveBeenCalled(); expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("请重新发布并公开逐题得分"));
  });

  it("可发布草稿确认公开内容和题目复核路径，执行仍经过近期身份验证", async () => {
    workflow.publishRelease({ id: "draft", version: 3, appeal_window: { enabled: true }, visibility_policy: { show_question_scores: true } } as unknown as ScoreRelease);
    const confirmation = mocks.confirm.mock.calls[0][0];
    expect(renderToStaticMarkup(confirmation.content)).toContain("学生可在开放时段内从逐题详情申请");
    await act(async () => confirmation.onOk());
    expect(mocks.stepUp).toHaveBeenCalled(); expect(mocks.publish).toHaveBeenCalledWith("draft");
  });
});

describe("一步发布、身份验证取消与更正", () => {
  let root: Root; let host: HTMLDivElement; let workflow: ReturnType<typeof useScoreReleaseWorkflow>;
  const exam = { id: "exam", name: "本次考试", appeal_enabled: false } as unknown as Exam;
  function Harness() {
    workflow = useScoreReleaseWorkflow({ selectedExamId: "exam", selectedExam: exam, educationStage: "junior", runAction: mocks.run, reload: mocks.reload, onPreview: mocks.preview });
    return null;
  }
  beforeEach(async () => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.run.mockImplementation(async (_key: string, action: () => Promise<void>) => { try { await action(); } catch (error) { if (!(error instanceof mocks.Cancelled)) throw error; } });
    mocks.create.mockResolvedValue({ score_release: { id: "draft-1", version: 2 } });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    await act(async () => root.render(<Harness />));
    await act(async () => workflow.openPublish());
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  // 取消身份验证时草稿已生成；重试必须发布同一份草稿，不能每点一次就多占一个版本号。
  it("身份验证取消后保留草稿，重试复用同一份并重读页面", async () => {
    mocks.stepUp.mockRejectedValueOnce(new mocks.Cancelled()).mockImplementation(async ({ action }: { action: () => Promise<void> }) => action());
    await act(async () => workflow.submitPublish());
    expect(mocks.reload).toHaveBeenCalledTimes(1);
    expect(mocks.publish).not.toHaveBeenCalled();
    await act(async () => workflow.submitPublish());
    expect(mocks.create).toHaveBeenCalledTimes(1);
    expect(mocks.publish).toHaveBeenCalledWith("draft-1");
    expect(mocks.create.mock.calls[0][1].visibility_policy).toMatchObject({ show_exact_rank: false, show_percentile: false });
  });

  it("义务教育学段公开排名必须写原因，原因随发布说明保存", async () => {
    mocks.stepUp.mockImplementation(async ({ action }: { action: () => Promise<void> }) => action());
    await act(async () => { workflow.choosePreset("custom"); });
    await act(async () => { workflow.setVisibility(current => ({ ...current, show_exact_rank: true })); });
    await act(async () => workflow.submitPublish());
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("不排名、不公布"));
    await act(async () => { workflow.setRankReason("家长会需要分层辅导"); });
    await act(async () => workflow.submitPublish());
    expect(mocks.create).toHaveBeenCalledWith("exam", expect.objectContaining({ reason: "首次发布本场成绩；公开排名原因：家长会需要分层辅导" }));
  });

  // 预览草稿生成期间重新登录时，迟到的回执不能在新会话里打开上一位账号的学生视角。
  it("预览回执迟到且会话已变化时不打开学生视角", async () => {
    let finish!: (value: unknown) => void;
    mocks.create.mockReturnValue(new Promise(resolve => { finish = resolve; }));
    let pending!: Promise<void>;
    mocks.run.mockImplementation((_key: string, action: () => Promise<void>) => { pending = action(); return pending; });
    await act(async () => { workflow.previewPublish(); });
    const before = mocks.session;
    mocks.session = { scope: "t:other", generation: 2 };
    try {
      await act(async () => { finish({ score_release: { id: "late", version: 2 } }); await pending; });
      expect(mocks.preview).not.toHaveBeenCalled();
    } finally { mocks.session = before; }
    mocks.create.mockResolvedValue({ score_release: { id: "draft-2", version: 3 } });
    mocks.run.mockImplementation(async (_key: string, action: () => Promise<void>) => action());
    await act(async () => { workflow.setRankReason(""); });
    await act(async () => { workflow.choosePreset("question"); });
    await act(async () => { workflow.previewPublish(); });
    expect(mocks.preview).toHaveBeenCalledWith(expect.objectContaining({ id: "draft-2" }));
  });

  it("发布更正时没有草稿才生成复评结果版本，已有草稿直接发布", async () => {
    mocks.stepUp.mockImplementation(async ({ action }: { action: () => Promise<void> }) => action());
    mocks.createRegrade.mockResolvedValue({ score_release: { id: "regrade-draft", version: 3 } });
    const job = { id: "job-1", reason_text: "答案更正", affected_count: 2 } as unknown as RegradeJob;
    await act(async () => workflow.openCorrection({ job }));
    await act(async () => workflow.publishCorrection());
    expect(mocks.createRegrade).toHaveBeenCalledWith("job-1", expect.objectContaining({ reason: "题目复评完成：答案更正" }));
    expect(mocks.publish).toHaveBeenLastCalledWith("regrade-draft");
    mocks.createRegrade.mockClear();
    await act(async () => workflow.openCorrection({ job, draft: { id: "existing", version: 4 } as unknown as ScoreRelease }));
    await act(async () => workflow.publishCorrection());
    expect(mocks.createRegrade).not.toHaveBeenCalled();
    expect(mocks.publish).toHaveBeenLastCalledWith("existing");
    // 服务端复用的结果版本已由他人发布时，只结束弹窗并重读，不再发起第二次发布。
    mocks.publish.mockClear();
    mocks.createRegrade.mockResolvedValue({ score_release: { id: "already", version: 5, status: "published" } });
    await act(async () => workflow.openCorrection({ job }));
    await act(async () => workflow.publishCorrection());
    expect(mocks.publish).not.toHaveBeenCalled();
    expect(workflow.correction).toBeNull();
  });
});
