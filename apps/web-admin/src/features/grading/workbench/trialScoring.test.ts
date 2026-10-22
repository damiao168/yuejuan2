import { describe, expect, it } from "vitest";
import { trialSamplesFinished, trialScoreDifference, validateTrialDraft } from "./trialScoring";
import type { ScoringTrialSample, ScoringTrialSampleRole } from "../../../api/scoringTrials";

const ids = ["s1", "s2", "s3", "s4", "s5"];
const items = ids.map(id => ({ answer_segment_id: id, question_id: "q1", state: "review" }));
const scores = Object.fromEntries(ids.map(id => [id, 2]));
const roles = Object.fromEntries(ids.map(id => [id, ["representative", "boundary", "incomplete_evidence"] as ScoringTrialSampleRole[]]));
const samples = ids.map(id => ({ segment_id: id, human_score: 2, max_score: 5, roles: [], result: { status: "succeeded", suggested_score: 3, evidence: [], risk_flags: [] } })) satisfies ScoringTrialSample[];

describe("本场试改事实边界", () => {
  it("同题、人工基准和三类覆盖齐备才允许保存", () => {
    expect(validateTrialDraft("q1", ids, items, scores, roles, 5)).toBeNull();
    expect(validateTrialDraft("q1", ids.slice(0, 4), items, scores, roles, 5)).not.toBeNull();
    expect(validateTrialDraft("q1", ids, [{ ...items[0], question_id: "q2" }, ...items.slice(1)], scores, roles, 5)).not.toBeNull();
    expect(validateTrialDraft("q1", ids, items, { ...scores, s1: 6 }, roles, 5)).not.toBeNull();
    expect(validateTrialDraft("q1", ids, items, { ...scores, s1: Number.NaN }, roles, 5)).not.toBeNull();
    expect(validateTrialDraft("q1", ids, items, scores, {}, 5)).not.toBeNull();
  });
  it("未返回、执行中和未知结果不能借全场计数判为完成", () => {
    expect(trialSamplesFinished(samples)).toBe(true);
    expect(trialSamplesFinished([{ ...samples[0], result: undefined }, ...samples.slice(1)])).toBe(false);
    expect(trialSamplesFinished([{ ...samples[0], result: { ...samples[0].result, status: "processing" } }, ...samples.slice(1)])).toBe(false);
    expect(trialSamplesFinished([{ ...samples[0], result: { ...samples[0].result, status: "future_status" } }, ...samples.slice(1)])).toBe(false);
    expect(trialSamplesFinished([{ ...samples[0], result: { ...samples[0].result, status: "failed" } }, ...samples.slice(1)])).toBe(true);
  });
  it("分差只计算本次成功建议，失败或缺分保持未知", () => {
    expect(trialScoreDifference(samples[0])).toBe(1);
    expect(trialScoreDifference({ ...samples[0], result: undefined })).toBeNull();
    expect(trialScoreDifference({ ...samples[0], result: { ...samples[0].result, status: "failed", suggested_score: 0 } })).toBeNull();
    expect(trialScoreDifference({ ...samples[0], result: { ...samples[0].result, suggested_score: undefined } })).toBeNull();
  });
});
