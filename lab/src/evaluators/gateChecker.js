import { readFileSync } from "node:fs";

export function loadGateConfig(filePath) {
  return JSON.parse(readFileSync(filePath, "utf8"));
}

function metric(report, key) {
  return report.metrics?.[key];
}

function requireMetric(reasons, report, key, predicate, message) {
  const value = metric(report, key);
  if (!Number.isFinite(value) || !predicate(value)) {
    reasons.push(`${message}; actual ${key}=${value}`);
  }
}

export function checkGate(report, level, config) {
  const gate = config[level];
  if (!gate) throw new Error(`Unknown release gate level: ${level}`);
  const reasons = [];

  if (!Number.isInteger(report.sample_count) || report.sample_count < 0 || report.sample_count < gate.minimum_samples) {
    reasons.push(`sample_count must be >= ${gate.minimum_samples}; actual ${report.sample_count}`);
  }
  if (report.metrics?.sample_count !== report.sample_count || !Array.isArray(report.results) || report.results.length !== report.sample_count) {
    reasons.push("report sample counts and result rows must agree");
  }
  for (const key of ["review_trigger_recall", "prompt_injection_detection", "essay_discussion_review_rate", "ocr_low_confidence_review_rate", "high_score_recall", "low_score_recall"]) {
    const evidence = report.metrics?.recall_evidence?.[key];
    if (!evidence || !Number.isInteger(evidence.denominator) || evidence.denominator > report.sample_count || evidence.denominator < Math.max(1, gate.minimum_positive_samples ?? 1) ||
        !Number.isInteger(evidence.numerator) || evidence.numerator < 0 || evidence.numerator > evidence.denominator ||
        evidence.status !== "evaluated" || metric(report, key) !== evidence.numerator / evidence.denominator) {
      reasons.push(`${key} requires at least ${Math.max(1, gate.minimum_positive_samples ?? 1)} evaluated positive examples and consistent numerator/denominator`);
    }
  }
  if (level !== "dev" && (report.model_info?.mock !== false || report.adapter === "mock" ||
      !/^[a-f0-9]{64}$/.test(report.dataset_sha256 ?? "") || report.dataset_kind !== "real" ||
      report.results?.some((item) => item.synthetic !== false || item.risk_flags?.includes("MOCK_OUTPUT")))) {
    reasons.push("pilot/production require a real adapter and identified real dataset evidence");
  }
  requireMetric(reasons, report, "schema_validity_rate", (value) => value >= gate.schema_validity_rate, "schema validity gate failed");
  requireMetric(reasons, report, "evidence_validity_rate", (value) => value >= gate.evidence_validity_rate, "evidence validity gate failed");
  requireMetric(reasons, report, "adjacent_agreement", (value) => value >= gate.adjacent_agreement, "adjacent agreement gate failed");
  requireMetric(reasons, report, "review_trigger_recall", (value) => value >= gate.review_trigger_recall, "review trigger recall gate failed");
  requireMetric(reasons, report, "prompt_injection_detection", (value) => value >= gate.prompt_injection_detection, "prompt injection detection gate failed");
  requireMetric(reasons, report, "essay_discussion_review_rate", (value) => value >= gate.essay_discussion_review_rate, "essay/discussion review gate failed");
  requireMetric(reasons, report, "ocr_low_confidence_review_rate", (value) => value >= gate.ocr_low_confidence_review_rate, "OCR low confidence review gate failed");
  if (gate.mae !== null) {
    requireMetric(reasons, report, "mae", (value) => value <= gate.mae, "MAE gate failed");
  }
  if (gate.high_score_recall !== null) {
    requireMetric(reasons, report, "high_score_recall", (value) => value >= gate.high_score_recall, "high score recall gate failed");
  }
  if (gate.low_score_recall !== null) {
    requireMetric(reasons, report, "low_score_recall", (value) => value >= gate.low_score_recall, "low score recall gate failed");
  }
  if (gate.require_no_score_above_max && metric(report, "no_score_above_max") !== true) {
    reasons.push("no_score_above_max must be true");
  }
  if (gate.require_mock_marked && metric(report, "mock_marked_rate") !== 1) {
    reasons.push("mock_marked_rate must be 1");
  }
  if (gate.require_evidence_verifier && metric(report, "evidence_verifier_ran") !== true) {
    reasons.push("evidence_verifier_ran must be true");
  }

  return {
    level,
    passed: reasons.length === 0,
    reasons
  };
}
