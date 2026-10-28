import { describe, expect, it } from "vitest";
import { evaluationReportExample } from "./governanceMaterials";
import { evaluationMaterialMatchesRun, parseEvaluationRunMaterial } from "./evaluationReportImport";
import type { EvaluationRun } from "../../api/modelGovernance";

describe("已有评测材料预填边界", () => {
  it("只读取批次字段，不信任文件的学校或自动登记候选", () => {
    const parsed = parseEvaluationRunMaterial(JSON.stringify({ ...evaluationReportExample, tenant_id: "another-school", dataset_sha256: "a".repeat(64), authorization_reference: "授权记录-1" }));
    expect(parsed.run).not.toHaveProperty("tenant_id");
    expect(parsed.run).not.toHaveProperty("candidates");
    expect(parsed.candidateCount).toBe(1);
    expect(parsed.run.dataset_sha256).toBe("a".repeat(64));
    expect(parsed.candidates[0].total_cost_micros).toBe(0);
    expect(parsed.candidates[0]).not.toHaveProperty("tenant_id");
  });
  it("拒绝模板占位哈希、负数和缺失授权，不静默生成事实", () => {
    expect(() => parseEvaluationRunMaterial(JSON.stringify(evaluationReportExample))).toThrow("SHA-256");
    expect(() => parseEvaluationRunMaterial(JSON.stringify({ ...evaluationReportExample, dataset_sha256: "a".repeat(64), sample_count: -1 }))).toThrow("正整数");
    expect(() => parseEvaluationRunMaterial(JSON.stringify({ ...evaluationReportExample, dataset_sha256: "a".repeat(64), authorization_reference: "" }))).toThrow("authorization_reference");
  });
  it("候选不能超出报告样本、重复次数或教师核对范围", () => {
    const base = { ...evaluationReportExample, dataset_sha256: "a".repeat(64), authorization_reference: "approval-1" };
    const candidate = base.candidates[0];
    expect(() => parseEvaluationRunMaterial(JSON.stringify({ ...base, candidates: [{ ...candidate, teacher_accepted_samples: 31 }] }))).toThrow("超出有效范围");
    expect(() => parseEvaluationRunMaterial(JSON.stringify({ ...base, candidates: [{ ...candidate, evaluated_samples: 29 }] }))).toThrow("样本范围");
    expect(() => parseEvaluationRunMaterial(JSON.stringify({ ...base, candidates: [{ ...candidate, stable_repeat_samples: 31 }] }))).toThrow("超出有效范围");
    expect(() => parseEvaluationRunMaterial(JSON.stringify({ ...base, candidates: [candidate, candidate] }))).toThrow("重复登记");
  });
  it("同批次编号也必须核对哈希、授权和样本范围才能预填候选", () => {
    const material = parseEvaluationRunMaterial(JSON.stringify({ ...evaluationReportExample, dataset_sha256: "a".repeat(64), authorization_reference: "approval-1" }));
    const run = { ...material.run, id: "run-1", status: "draft", candidates: [], created_at: "2026-10-28" } as EvaluationRun;
    expect(evaluationMaterialMatchesRun(material, run)).toBe(true);
    expect(evaluationMaterialMatchesRun(material, { ...run, dataset_sha256: "b".repeat(64) })).toBe(false);
    expect(evaluationMaterialMatchesRun(material, { ...run, authorization_reference: "approval-2" })).toBe(false);
    expect(evaluationMaterialMatchesRun(material, { ...run, repeat_count: 3 })).toBe(false);
  });
});
