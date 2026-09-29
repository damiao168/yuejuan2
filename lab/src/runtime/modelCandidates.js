import { readFileSync } from "node:fs";
import { dirname, isAbsolute, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { LAB_ROOT } from "./localRuntime.js";

const here = dirname(fileURLToPath(import.meta.url));
export const DEFAULT_MODEL_CANDIDATES = join(here, "../../config/model-candidates.json");

export function loadModelCandidates(path = DEFAULT_MODEL_CANDIDATES) {
  return JSON.parse(readFileSync(path, "utf8"));
}

export function validateModelCandidates(registry) {
  const errors = [];
  if (registry?.schema_version !== "model-candidates-v1") errors.push("unsupported model candidate schema_version");
  if (!Array.isArray(registry?.candidates) || registry.candidates.length === 0) return { valid: false, errors: [...errors, "candidates must be non-empty"] };
  const ids = new Set();
  for (const [index, candidate] of registry.candidates.entries()) {
    const label = `candidates[${index}]`;
    if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) {
      errors.push(`${label} must be an object`);
      continue;
    }
    if (!candidate.candidate_id) errors.push(`${label}.candidate_id is required`);
    if (ids.has(candidate.candidate_id)) errors.push(`duplicate candidate_id: ${candidate.candidate_id}`);
    ids.add(candidate.candidate_id);
    if (!/^https:\/\//.test(candidate.url ?? "")) errors.push(`${label}.url must use https`);
    if (!Number.isInteger(candidate.expected_bytes) || candidate.expected_bytes <= 0) errors.push(`${label}.expected_bytes must be positive`);
    if (!/^[a-f0-9]{64}$/.test(candidate.expected_sha256 ?? "")) errors.push(`${label}.expected_sha256 is required`);
    // 解析规范化相对路径后再判定范围；下载目标约定在 lab 内，不能只查字符串前缀。
    const modelPath = resolve(LAB_ROOT, candidate.model_path ?? "");
    const rel = relative(LAB_ROOT, modelPath);
    if (!candidate.model_path || isAbsolute(candidate.model_path) || rel.startsWith("..") || isAbsolute(rel)) {
      errors.push(`${label}.model_path must stay inside lab`);
    }
  }
  return { valid: errors.length === 0, errors };
}

export function getModelCandidate(candidateId, registry = loadModelCandidates()) {
  const validation = validateModelCandidates(registry);
  if (!validation.valid) throw new Error(`Invalid model candidate registry: ${validation.errors.join("; ")}`);
  const candidate = registry.candidates.find((item) => item.candidate_id === candidateId);
  if (!candidate) throw new Error(`Unknown model candidate: ${candidateId}`);
  return { ...candidate, absolute_model_path: resolve(LAB_ROOT, candidate.model_path) };
}
