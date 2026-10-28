import type { AddEvaluationCandidateInput, CreateEvaluationRunInput, EvaluationRun } from "../../api/modelGovernance";

export interface EvaluationRunMaterial {
  run: Omit<CreateEvaluationRunInput, "tenant_id">;
  candidates: AddEvaluationCandidateInput[];
  candidateCount: number;
}

export function evaluationMaterialMatchesRun(material: EvaluationRunMaterial, run: EvaluationRun) {
  // 共同元数据一致才能沿用候选；同名报告或相同批次编号不能替代证据范围核对。
  const fields = ["run_key", "dataset_reference", "dataset_sha256", "evidence_class", "subject", "grade", "question_type", "modality", "sample_count", "repeat_count"] as const;
  return fields.every(key => material.run[key] === run[key]) && (material.run.authorization_reference ?? "") === (run.authorization_reference ?? "");
}

export function parseEvaluationRunMaterial(text: string): EvaluationRunMaterial {
  const value: unknown = JSON.parse(text);
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("报告须为 JSON 对象");
  const data = value as Record<string, unknown>;
  const string = (key: string) => {
    if (typeof data[key] !== "string" || !data[key].trim()) throw new Error(`报告缺少有效字段：${key}`);
    return data[key].trim();
  };
  const count = (key: string) => {
    const number = data[key];
    if (typeof number !== "number" || !Number.isSafeInteger(number) || number < 1) throw new Error(`报告字段 ${key} 须为正整数`);
    return number;
  };
  const evidence = string("evidence_class");
  const modality = string("modality");
  if (evidence !== "protocol_fixture" && evidence !== "authorized_frozen_set") throw new Error("报告证据类别无效");
  if (modality !== "text" && modality !== "image") throw new Error("报告模态须为 text 或 image");
  const hash = string("dataset_sha256");
  if (!/^[a-f0-9]{64}$/i.test(hash)) throw new Error("数据集 SHA-256 须为实际的 64 位十六进制值");
  const sampleCount = count("sample_count");
  const repeatCount = count("repeat_count");
  if (data.candidates !== undefined && !Array.isArray(data.candidates)) throw new Error("报告候选须为数组");
  const candidates = (data.candidates ?? []) as unknown[];
  const parsedCandidates = candidates.map((candidate, index): AddEvaluationCandidateInput => {
    if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) throw new Error(`候选 ${index + 1} 格式无效`);
    const fields = candidate as Record<string, unknown>;
    const text = (key: string) => {
      if (typeof fields[key] !== "string" || !fields[key].trim()) throw new Error(`候选 ${index + 1} 缺少 ${key}`);
      return fields[key].trim();
    };
    const amount = (key: string, max = Number.MAX_SAFE_INTEGER) => {
      const value = fields[key];
      if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > max) throw new Error(`候选 ${index + 1} 的 ${key} 超出有效范围`);
      return value;
    };
    const evaluated = amount("evaluated_samples", sampleCount);
    const reviewed = amount("teacher_reviewed_samples", sampleCount);
    if (evaluated !== sampleCount || reviewed !== (evidence === "authorized_frozen_set" ? sampleCount : 0)) throw new Error(`候选 ${index + 1} 的样本范围与批次不一致`);
    const comparisons = amount("repeat_comparisons", sampleCount * Math.max(0, repeatCount - 1));
    return {
      model_config_id: text("model_config_id"), prompt_version: text("prompt_version"), rubric_version: text("rubric_version"), reason: text("reason"),
      evaluated_samples: evaluated, teacher_reviewed_samples: reviewed, teacher_accepted_samples: amount("teacher_accepted_samples", reviewed),
      serious_error_samples: amount("serious_error_samples", sampleCount), evidence_valid_samples: amount("evidence_valid_samples", sampleCount),
      repeat_comparisons: comparisons, stable_repeat_samples: amount("stable_repeat_samples", comparisons), p95_latency_ms: amount("p95_latency_ms"), total_cost_micros: amount("total_cost_micros")
    };
  });
  if (new Set(parsedCandidates.map(candidate => candidate.model_config_id)).size !== parsedCandidates.length) throw new Error("同一报告不能重复登记相同模型的候选");
  // 只预填白名单字段；文件不能指定学校或直接创建、冻结和批准记录。
  return { run: {
    run_key: string("run_key"), display_name: string("display_name"), dataset_reference: string("dataset_reference"), dataset_sha256: hash.toLowerCase(),
    evidence_class: evidence, authorization_reference: evidence === "authorized_frozen_set" ? string("authorization_reference") : undefined,
    subject: string("subject"), grade: string("grade"), question_type: string("question_type"), modality,
    sample_count: sampleCount, repeat_count: repeatCount, reason: string("reason")
  }, candidates: parsedCandidates, candidateCount: parsedCandidates.length };
}
