// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../../api/client";
import type { MathRubricScoreResponse, MathUnderstandingResponse } from "../../../../api/mathUnderstanding";
import { useMathWorkbenchEvidence } from "./useMathWorkbenchEvidence";

const api = vi.hoisted(() => ({ understanding: vi.fn(), score: vi.fn() }));
vi.mock("../../../../api/mathUnderstanding", () => ({ getMathUnderstanding: api.understanding, getMathRubricScore: api.score }));

function fixture(segmentId = "segment-A", subjectCode = "mathematics") {
  const understanding = { artifact: {
    id: `artifact-${segmentId}`, answer_segment_id: segmentId, subject_code: subjectCode,
    exam_question_snapshot_id: `snapshot-${segmentId}`, version: 3, stage: "verified", correction_revision: 2
  }, correction_revision: 0 } as unknown as MathUnderstandingResponse;
  const score = {
    schema_version: "math-rubric-score-v1", scope: "teacher_suggestion_only", artifact_id: understanding.artifact.id,
    artifact_version: 3, correction_revision: 0, verified_correction_revision: 2,
    exam_question_snapshot_id: `snapshot-${segmentId}`
  } as MathRubricScoreResponse;
  return { understanding, score };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("math evidence projection and failure recovery (mock API)", () => {
  let root: Root;
  let evidence: ReturnType<typeof useMathWorkbenchEvidence>;

  beforeEach(() => {
    vi.resetAllMocks();
    vi.useFakeTimers();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    root = createRoot(document.createElement("div"));
  });
  afterEach(async () => { await act(async () => root.unmount()); vi.useRealTimers(); });

  function Probe({ segmentId, subjectCode }: Readonly<{ segmentId: string; subjectCode: string }>) {
    evidence = useMathWorkbenchEvidence(segmentId, subjectCode, `snapshot-${segmentId}`);
    return null;
  }
  async function render(segmentId = "segment-A", subjectCode = "mathematics") {
    await act(async () => root.render(<Probe segmentId={segmentId} subjectCode={subjectCode} />));
  }

  it.each([
    { name: "verified uncorrected evidence", stage: "verified", correctionRevision: 0, phase: "ready" },
    { name: "recognition awaiting verification", stage: "recognition", correctionRevision: 0, phase: "pending" },
    { name: "verified evidence with local correction", stage: "verified", correctionRevision: 1, phase: "pending" }
  ] as const)("keeps $name phase with a matching score", async ({ stage, correctionRevision, phase }) => {
    const current = fixture();
    current.understanding.artifact.stage = stage;
    current.understanding.correction_revision = correctionRevision;
    current.score.correction_revision = correctionRevision;
    api.understanding.mockResolvedValue(current.understanding);
    api.score.mockResolvedValue(current.score);
    await render();
    expect(evidence.state.phase).toBe(phase);
    expect(evidence.state.understanding).toBe(current.understanding);
    expect(evidence.state.score).toBe(current.score);
    expect(evidence.state.message).toBe(phase === "pending" ? "校正/识别已保存，等待数学步骤重新验证。旧建议不可采纳。" : "");
  });

  it("keeps raced scoring evidence conflicting without exposing its score", async () => {
    const current = fixture();
    api.understanding.mockResolvedValue(current.understanding);
    api.score.mockResolvedValue({ ...current.score, artifact_version: 4 });
    await render();
    expect(evidence.state).toMatchObject({ phase: "conflict", score: null, message: "数学证据在读取期间已更新，请刷新。" });
    expect(evidence.state.understanding).toBe(current.understanding);
  });

  it.each([
    { name: "missing score", cause: new ApiClientError(404, "not_found", "missing"), phase: "unavailable", message: "暂无线性化数学证据，请按原图阅卷。" },
    { name: "changed evidence", cause: new ApiClientError(409, "math_revision_conflict", "changed"), phase: "conflict", message: "答题图片或数学证据已变化，等待新证据或刷新。旧建议不可采纳。" },
    { name: "server failure", cause: new ApiClientError(503, "unavailable", "internal details"), phase: "unavailable", message: "系统暂时无法完成操作，请稍后重试。" },
    { name: "ordinary failure", cause: new Error("score unavailable"), phase: "unavailable", message: "数学证据暂不可用，请按原图人工判定。" }
  ] as const)("retains understanding after $name during the score read", async ({ cause, phase, message }) => {
    // 理解先保存到本次读取结果；后续评分失败不能抹掉这份已取得的理解投影。
    const current = fixture();
    api.understanding.mockResolvedValue(current.understanding);
    api.score.mockRejectedValue(cause);
    await render();
    expect(evidence.state).toMatchObject({ phase, message, score: null });
    expect(evidence.state.understanding).toBe(current.understanding);
  });

  it("keeps physics evidence ready without reading a mathematical rubric score", async () => {
    const current = fixture("segment-A", "physics");
    api.understanding.mockResolvedValue(current.understanding);
    await render("segment-A", "physics");
    expect(evidence.state).toMatchObject({ phase: "ready", score: null, message: "" });
    expect(api.score).not.toHaveBeenCalled();
  });

  it("does not replace a new segment with an old response that ignores abort", async () => {
    // mock 故意忽略 abort 并晚到，保护请求代次和当前答题区共同拒绝旧结果的边界。
    const first = fixture();
    const second = fixture("segment-B");
    const old = deferred<MathUnderstandingResponse>();
    api.understanding.mockImplementation((segmentId) => segmentId === "segment-A" ? old.promise : Promise.resolve(second.understanding));
    api.score.mockImplementation((segmentId) => Promise.resolve(segmentId === "segment-A" ? first.score : second.score));
    await render();
    const oldSignal = api.understanding.mock.calls[0][1] as AbortSignal;
    await render("segment-B");
    expect(oldSignal.aborted).toBe(true);
    expect(evidence.state.segmentId).toBe("segment-B");
    await act(async () => { old.resolve(first.understanding); });
    expect(evidence.state.segmentId).toBe("segment-B");
    expect(evidence.state.understanding).toBe(second.understanding);
    expect(evidence.state.score).toBe(second.score);
    expect(evidence.state.phase).toBe("ready");
  });
});
