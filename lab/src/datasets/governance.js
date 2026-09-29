import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
export const DEFAULT_SOURCE_REGISTRY = join(here, "../../config/dataset-sources.json");

const PII_DETECTORS = Object.freeze([
  { id: "email", pattern: /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i },
  { id: "cn_mobile", pattern: /(?<!\d)1[3-9]\d{9}(?!\d)/ },
  { id: "cn_id_card", pattern: /(?<!\d)\d{17}[0-9Xx](?!\d)/ },
  { id: "student_number_label", pattern: /(?:学号|student\s*(?:id|number))\s*[:：]?\s*[A-Z0-9-]{4,}/i },
  { id: "name_label", pattern: /(?:\u59d3\u540d|\u5b66\u751f\u59d3\u540d)\s*[:\uff1a]\s*[\p{Script=Han}\u00b7]{2,8}|(?:^|[\r\n{,])\s*name\s*:\s*[A-Z][\p{L}'-]*(?:\s+[A-Z][\p{L}'-]*){0,3}\s*(?=$|[\r\n,;}])/imu },
  { id: "phone_label", pattern: /(?:电话|手机号|phone|mobile)\s*[:：]?\s*[+\d][\d\s-]{6,}/i }
]);

export function loadDatasetSourceRegistry(path = DEFAULT_SOURCE_REGISTRY) {
  return JSON.parse(readFileSync(path, "utf8"));
}

export function validateDatasetSourceRegistry(registry) {
  const errors = [];
  if (registry?.schema_version !== "dataset-source-registry-v1") errors.push("unsupported source registry schema_version");
  if (!Array.isArray(registry?.sources)) return { valid: false, errors: [...errors, "sources must be an array"] };
  const ids = new Set();
  for (const [index, source] of registry.sources.entries()) {
    const label = `sources[${index}]`;
    if (!source || typeof source !== "object" || Array.isArray(source)) {
      errors.push(`${label} must be an object`);
      continue;
    }
    if (!source.source_id) errors.push(`${label}.source_id is required`);
    if (ids.has(source.source_id)) errors.push(`duplicate source_id: ${source.source_id}`);
    ids.add(source.source_id);
    if (!/^https:\/\//.test(source.homepage ?? "") && !String(source.homepage ?? "").startsWith("lab/")) {
      errors.push(`${label}.homepage must be https or lab-local`);
    }
    if (!["approved", "review_required", "prohibited"].includes(source.license_status)) {
      errors.push(`${label}.license_status is unsupported`);
    }
    if (!Array.isArray(source.allowed_uses)) errors.push(`${label}.allowed_uses must be an array`);
    if (source.license_status === "approved" && !["synthetic_development", "external_real_benchmark", "pilot_in_domain_gold"].includes(source.evidence_scope)) {
      errors.push(`${label}.evidence_scope is required for approved sources`);
    }
    if (source.license_status !== "approved" && source.allowed_uses?.length > 0) {
      errors.push(`${label} cannot allow use before license approval`);
    }
  }
  return { valid: errors.length === 0, errors };
}

export function getApprovedDatasetSource(sourceId, intendedUse, registry = loadDatasetSourceRegistry()) {
  const validation = validateDatasetSourceRegistry(registry);
  if (!validation.valid) throw new Error(`Invalid dataset source registry: ${validation.errors.join("; ")}`);
  const source = registry.sources.find((item) => item.source_id === sourceId);
  if (!source) throw new Error(`Unknown dataset source: ${sourceId}`);
  if (source.license_status !== "approved") throw new Error(`Dataset source is not license-approved: ${sourceId}`);
  if (!source.allowed_uses.includes(intendedUse)) throw new Error(`Dataset source does not allow ${intendedUse}: ${sourceId}`);
  return source;
}

export function scanSensitiveText(value) {
  const text = String(value ?? "");
  return PII_DETECTORS.filter((detector) => detector.pattern.test(text)).map((detector) => detector.id);
}

function collectSensitiveFindings(sample) {
  const findings = [];
  for (const field of ["question_text", "answer_text", "human_rationale"]) {
    for (const detector of scanSensitiveText(sample[field])) findings.push({ field, detector });
  }
  const rubricText = JSON.stringify(sample.rubric ?? {});
  for (const detector of scanSensitiveText(rubricText)) findings.push({ field: "rubric", detector });
  return findings;
}

// 以题目和量规版本组成隔离组；按记录随机拆分会让同题作答跨越训练与测试集。
export function questionGroupKey(sample) {
  const rubricVersion = sample.rubric_version ?? sample.rubric?.rubric_version;
  if (!rubricVersion) throw new Error(`sample ${sample.sample_id ?? "unknown"} lacks rubric_version`);
  if (sample.question_id) return `${sample.question_id}::${rubricVersion}`;
  if (sample.synthetic === true && sample.question_text) {
    const derived = createHash("sha256").update(sample.question_text.trim()).digest("hex").slice(0, 20);
    return `synthetic-question-${derived}::${rubricVersion}`;
  }
  throw new Error(`real sample ${sample.sample_id ?? "unknown"} requires question_id`);
}

export function validateGovernedSamples(samples, source) {
  const errors = [];
  const ids = new Set();
  samples.forEach((sample, index) => {
    const label = `samples[${index}]`;
    if (!sample.sample_id) errors.push(`${label}.sample_id is required`);
    if (ids.has(sample.sample_id)) errors.push(`duplicate sample_id: ${sample.sample_id}`);
    ids.add(sample.sample_id);
    if (!sample.rubric?.rubric_version) errors.push(`${label}.rubric.rubric_version is required`);
    if (source.source_type === "synthetic" && sample.synthetic !== true) errors.push(`${label} must set synthetic=true`);
    if (source.source_type !== "synthetic") {
      if (sample.synthetic === true) errors.push(`${label} cannot be synthetic for source ${source.source_id}`);
      if (sample.privacy_status !== "anonymized") errors.push(`${label}.privacy_status must be anonymized`);
      if (!sample.question_id) errors.push(`${label}.question_id is required for real data`);
    }
    const findings = collectSensitiveFindings(sample);
    for (const finding of findings) errors.push(`${label}.${finding.field} contains sensitive pattern: ${finding.detector}`);
    try { questionGroupKey(sample); } catch (error) { errors.push(error.message); }
  });
  return { valid: errors.length === 0, errors };
}

function hashOrder(seed, key) {
  return createHash("sha256").update(`${seed}\0${key}`).digest("hex");
}

export function splitByQuestionGroup(samples, options = {}) {
  const seed = options.seed ?? "edugrade-group-split-v1";
  const ratios = options.ratios ?? { train: 0.7, validation: 0.15, test: 0.15 };
  for (const name of ["train", "validation", "test"]) {
    if (!Number.isFinite(ratios[name]) || ratios[name] < 0 || ratios[name] > 1) {
      throw new Error(`split ratio ${name} must be between 0 and 1`);
    }
  }
  const ratioTotal = ratios.train + ratios.validation + ratios.test;
  if (Math.abs(ratioTotal - 1) > 1e-9) throw new Error("split ratios must total 1");
  const groups = new Map();
  for (const sample of samples) {
    const key = questionGroupKey(sample);
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(sample);
  }
  // 哈希排序让分组顺序只依赖 seed 与组键，不受输入记录排列影响。
  const ordered = [...groups.entries()].sort(([left], [right]) => hashOrder(seed, left).localeCompare(hashOrder(seed, right)));
  const groupCount = ordered.length;
  let validationCount = Math.round(groupCount * ratios.validation);
  let testCount = Math.round(groupCount * ratios.test);
  // 比例按组数取整；至少三组时给验证、测试各留一组，实际比例可偏离请求比例。
  if (groupCount >= 3) {
    validationCount = Math.max(1, validationCount);
    testCount = Math.max(1, testCount);
  }
  if (validationCount + testCount >= groupCount && groupCount > 0) {
    const excess = validationCount + testCount - groupCount + 1;
    if (validationCount >= testCount) validationCount -= excess;
    else testCount -= excess;
  }
  const trainCount = groupCount - validationCount - testCount;
  const allocations = {
    train: ordered.slice(0, trainCount),
    validation: ordered.slice(trainCount, trainCount + validationCount),
    test: ordered.slice(trainCount + validationCount)
  };
  const splits = Object.fromEntries(Object.entries(allocations).map(([name, entries]) => [name, entries.flatMap(([, records]) => records)]));
  const audit = auditSplitLeakage(splits);
  if (!audit.passed) throw new Error(`group leakage detected: ${audit.leaked_groups.join(", ")}`);
  return { seed, ratios, group_count: groupCount, split_group_counts: { train: trainCount, validation: validationCount, test: testCount }, splits };
}

export function auditSplitLeakage(splits) {
  const owners = new Map();
  const leaked = new Set();
  for (const [split, samples] of Object.entries(splits)) {
    for (const sample of samples) {
      const key = questionGroupKey(sample);
      if (owners.has(key) && owners.get(key) !== split) leaked.add(key);
      owners.set(key, split);
    }
  }
  return { passed: leaked.size === 0, leaked_groups: [...leaked].sort(), unique_groups: owners.size };
}

export function sha256Text(text) {
  return createHash("sha256").update(text).digest("hex");
}
