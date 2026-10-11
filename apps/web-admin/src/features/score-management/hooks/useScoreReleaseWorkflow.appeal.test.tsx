// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Exam } from "../../../api/exams";
import type { ScoreRelease, ScoreReleaseGate } from "../../../api/scoreReleases";
import { defaultReleaseVisibility, useScoreReleaseWorkflow } from "./useScoreReleaseWorkflow";

const mocks = vi.hoisted(() => ({ create: vi.fn(), publish: vi.fn(), error: vi.fn(), confirm: vi.fn(), run: vi.fn(), stepUp: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { error: mocks.error }, modal: { confirm: mocks.confirm } }) } }));
vi.mock("../../../api/scoreReleases", () => ({ createScoreRelease: mocks.create, publishScoreRelease: mocks.publish, revokeHighScorePaper: vi.fn() }));
vi.mock("../../../auth/stepUpContext", () => ({ useStepUp: () => ({ runWithStepUp: mocks.stepUp }) }));

describe("复核申请与成绩发布依赖", () => {
  let root: Root; let host: HTMLDivElement; let workflow: ReturnType<typeof useScoreReleaseWorkflow>;
  const exam = { id: "exam", name: "本次考试", appeal_enabled: true } as unknown as Exam;
  function Harness() {
    workflow = useScoreReleaseWorkflow({ selectedExamId: "exam", selectedExam: exam, gradeTotal: 1, releaseGate: { passed: true } as ScoreReleaseGate, runAction: mocks.run });
    return null;
  }
  beforeEach(async () => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.run.mockImplementation(async (_key: string, action: () => Promise<void>) => action());
    mocks.stepUp.mockImplementation(async ({ action }: { action: () => Promise<void> }) => action());
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    await act(async () => root.render(<Harness />));
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("开放复核却关闭逐题得分时阻止创建，不发送矛盾策略", async () => {
    await act(async () => { workflow.setReleaseReason("正式首次发布"); workflow.setReleaseVisibility({ ...defaultReleaseVisibility, show_question_scores: false }); });
    await act(async () => workflow.createRelease());
    expect(mocks.create).not.toHaveBeenCalled(); expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("必须公开逐题得分"));
  });

  it("逐题得分公开时允许创建，默认空白名单沿用服务端允许全部规则", async () => {
    await act(async () => workflow.setReleaseReason(" 正式首次发布 "));
    await act(async () => workflow.createRelease());
    expect(mocks.create).toHaveBeenCalledWith("exam", expect.objectContaining({ reason: "正式首次发布", visibility_policy: expect.objectContaining({ show_question_scores: true }), appeal_window: { enabled: true } }));
  });

  it("正式发布校验已冻结草稿，不能用当前表单公开策略掩盖历史矛盾配置", () => {
    workflow.publishRelease({ id: "old-draft", version: 2, appeal_window: { enabled: true }, visibility_policy: { show_question_scores: false } } as unknown as ScoreRelease);
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.publish).not.toHaveBeenCalled(); expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("请重新创建公开逐题得分的草稿"));
  });

  it("可发布草稿确认公开内容和题目复核路径，执行仍经过近期身份验证", async () => {
    workflow.publishRelease({ id: "draft", version: 3, appeal_window: { enabled: true }, visibility_policy: { show_question_scores: true } } as unknown as ScoreRelease);
    const confirmation = mocks.confirm.mock.calls[0][0];
    expect(renderToStaticMarkup(confirmation.content)).toContain("学生可从逐题详情申请复核");
    await act(async () => confirmation.onOk());
    expect(mocks.stepUp).toHaveBeenCalled(); expect(mocks.publish).toHaveBeenCalledWith("draft");
  });
});
