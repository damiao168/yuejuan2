export function compareModelSelectionReports(reports) {
  if (!Array.isArray(reports) || reports.length < 2) throw new Error("at least two model selection reports are required");
  const candidateIds = reports.map((report) => report?.candidate_id);
  if (candidateIds.some((id) => typeof id !== "string" || id.trim().length === 0) ||
      new Set(candidateIds).size !== candidateIds.length) {
    throw new Error("model selection reports require unique non-empty candidate ids");
  }
  // 数据内容、来源和证据用途必须一致，避免把不同难度或用途的报告直接排名。
  const datasetHashes = new Set(reports.map((report) => report.dataset?.sha256));
  if (datasetHashes.size !== 1 || datasetHashes.has(undefined)) throw new Error("model selection reports must use the same dataset hash");
  const scopes = new Set(reports.map((report) => report.evidence_scope ?? report.dataset?.evidence_scope ??
    (report.synthetic_only === true ? "synthetic_development" : "unqualified_real")));
  if (scopes.size !== 1) throw new Error("model selection reports must use the same evidence scope");
  const evidenceScope = [...scopes][0];
  const sourceIds = new Set(reports.map((report) => report.dataset?.source_id ??
    (report.synthetic_only === true ? "lab_synthetic" : undefined)));
  if (sourceIds.size !== 1 || sourceIds.has(undefined)) throw new Error("model selection reports must use the same dataset source");
  const syntheticFlags = new Set(reports.map((report) => report.synthetic_only === true));
  if (syntheticFlags.size !== 1) throw new Error("model selection reports disagree on synthetic status");
  const candidates = reports.map((report) => {
    const total = report.dataset.total_samples;
    const completed = report.operations.completed;
    const countsValid = Number.isInteger(total) && total > 0 && Number.isInteger(completed) && completed >= 0 &&
      Number.isInteger(report.operations.failed) && report.operations.failed >= 0;
    const completionRate = countsValid ? completed / total : 0;
    const isModel = report.model_info?.mock === false;
    const metricsValid = Number.isFinite(report.quality.mae) && report.quality.mae >= 0 &&
      Number.isFinite(report.operations.latency_ms_p95) && report.operations.latency_ms_p95 >= 0;
    // 质量指标只覆盖已完成样本，因此完成率和失败数单独作为候选资格门槛。
    const eligible = isModel && report.dataset?.complete_dataset !== false && completionRate === 1 && report.operations.failed === 0 &&
      report.quality.schema_validity_rate === 1 && report.quality.evidence_validity_rate === 1 && metricsValid;
    return {
      candidate_id: report.candidate_id,
      role: isModel ? "llm_candidate" : "deterministic_reference",
      eligible,
      completion_rate: completionRate,
      failures: report.operations.failed,
      mae_completed_samples: report.quality.mae,
      exact_agreement_completed_samples: report.quality.exact_agreement,
      schema_validity_completed_samples: report.quality.schema_validity_rate,
      evidence_validity_completed_samples: report.quality.evidence_validity_rate,
      p95_latency_ms: report.operations.latency_ms_p95
    };
  });
  const eligible = candidates.filter((candidate) => candidate.eligible)
    .sort((left, right) => left.mae_completed_samples - right.mae_completed_samples || left.p95_latency_ms - right.p95_latency_ms);
  const decisionStatus = eligible.length === 0 ? "no_eligible_local_model" :
    evidenceScope === "pilot_in_domain_gold" ? "pilot_real_gold_selection" :
      evidenceScope === "external_real_benchmark" ? "external_benchmark_only" : "preliminary_synthetic_baseline";
  return {
    schema_version: "model-selection-decision-v1",
    synthetic_only: reports[0].synthetic_only === true,
    evidence_scope: evidenceScope,
    dataset_source_id: [...sourceIds][0],
    dataset_sha256: reports[0].dataset.sha256,
    candidates,
    selected_local_llm: eligible[0]?.candidate_id ?? null,
    decision_status: decisionStatus,
    constraints: [
      "Quality metrics cover completed samples only; completion rate is a separate eligibility gate.",
      "Deterministic alias rules are a reference, not an LLM candidate.",
      "Synthetic selection must be repeated on governed real Gold data before pilot release."
    ]
  };
}

export function selectDiverseBenchmarkSamples(samples, limit) {
  if (limit === undefined || limit === null) return [...samples];
  if (!Number.isInteger(limit) || limit <= 0) throw new Error("benchmark sample limit must be a positive integer");
  if (limit >= samples.length) return [...samples];
  const groups = new Map();
  for (const sample of samples) {
    const key = `${sample.question_id ?? "unknown"}::${sample.rubric_version ?? sample.rubric?.rubric_version ?? "unknown"}`;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(sample);
  }
  // 按题目与量规版本分组轮流取样，避免截取文件前几行导致题型集中。
  const queues = [...groups.entries()].sort(([left], [right]) => left.localeCompare(right)).map(([, records]) => [...records]);
  const selected = [];
  while (selected.length < limit) {
    let added = false;
    for (const queue of queues) {
      if (queue.length === 0) continue;
      selected.push(queue.shift());
      added = true;
      if (selected.length === limit) break;
    }
    if (!added) break;
  }
  return selected;
}

export function qualifyPilotModelSelection(decision, realManifest, selectedCandidate = "qwen3_4b") {
  const reasons = [];
  if (!realManifest) return { passed: false, reasons: ["governed real Gold manifest is missing"] };
  if (realManifest.source?.evidence_scope !== "pilot_in_domain_gold") {
    reasons.push(`real Gold evidence_scope is ${realManifest.source?.evidence_scope ?? "missing"}, expected pilot_in_domain_gold`);
  }
  if (decision?.synthetic_only !== false) reasons.push("selection is synthetic-only");
  if (decision?.evidence_scope !== "pilot_in_domain_gold") {
    reasons.push(`selection evidence_scope is ${decision?.evidence_scope ?? "missing"}, expected pilot_in_domain_gold`);
  }
  if (decision?.dataset_source_id !== realManifest.source?.source_id) reasons.push("selection source does not match real Gold manifest");
  if (decision?.dataset_sha256 !== realManifest.files?.test?.sha256) reasons.push("selection dataset hash does not match frozen real Gold test split");
  if (decision?.selected_local_llm !== selectedCandidate) reasons.push(`selected local model is not ${selectedCandidate}`);
  if (decision?.decision_status !== "pilot_real_gold_selection") reasons.push("selection decision status is not pilot_real_gold_selection");
  return { passed: reasons.length === 0, reasons };
}
