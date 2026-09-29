import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { QUESTION_TYPES, SUBJECTS } from "./schemas/gradingSchema.js";

const matrixPath = fileURLToPath(new URL("../config/capability-matrix.json", import.meta.url));
const MODES = new Set(["rule_assisted", "hybrid_assisted", "llm_assisted", "shadow", "human_only"]);
const GRADERS = new Set(["rule", "rules_plus_llm", "llm", "human"]);
const DELIVERIES = new Set(["teacher_suggestion", "teacher_review", "shadow_only"]);
const REVIEW_POLICIES = new Set(["risk_based", "always"]);

export function loadCapabilityMatrix(path = matrixPath) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function validateRoute(route, label, errors) {
  if (!route || typeof route !== "object" || Array.isArray(route)) {
    errors.push(`${label} must be an object`);
    return;
  }
  if (!MODES.has(route.mode)) errors.push(`${label}.mode is unsupported: ${route.mode}`);
  if (!GRADERS.has(route.grader)) errors.push(`${label}.grader is unsupported: ${route.grader}`);
  if (!DELIVERIES.has(route.delivery)) errors.push(`${label}.delivery is unsupported: ${route.delivery}`);
  if (!REVIEW_POLICIES.has(route.review_policy)) {
    errors.push(`${label}.review_policy is unsupported: ${route.review_policy}`);
  }
  if (route.mode === "shadow" && route.delivery !== "shadow_only") {
    errors.push(`${label} shadow mode must use shadow_only delivery`);
  }
  if (["llm_assisted", "hybrid_assisted", "shadow"].includes(route.mode) && route.review_policy !== "always") {
    errors.push(`${label} LLM-backed routes must always require review in the local pilot`);
  }
}

export function validateCapabilityMatrix(matrix) {
  const errors = [];
  if (!matrix || typeof matrix !== "object" || Array.isArray(matrix)) {
    return { valid: false, errors: ["capability matrix must be an object"] };
  }
  if (matrix.schema_version !== "capability-matrix-v1") errors.push("unsupported capability matrix schema_version");
  if (typeof matrix.profile_id !== "string" || matrix.profile_id.length === 0) errors.push("profile_id is required");
  if (!Array.isArray(matrix.grade_levels) || matrix.grade_levels.length === 0) errors.push("grade_levels must be non-empty");
  if (matrix.final_grade_publication_allowed !== false) errors.push("final grade publication must be disabled");
  validateRoute(matrix.default_route, "default_route", errors);

  if (!Array.isArray(matrix.capabilities)) {
    errors.push("capabilities must be an array");
  } else {
    const keys = new Set();
    matrix.capabilities.forEach((route, index) => {
      const label = `capabilities[${index}]`;
      validateRoute(route, label, errors);
      if (route?.subject !== "*" && !SUBJECTS.includes(route?.subject)) {
        errors.push(`${label}.subject is unsupported: ${route?.subject}`);
      }
      if (!QUESTION_TYPES.includes(route?.question_type)) {
        errors.push(`${label}.question_type is unsupported: ${route?.question_type}`);
      }
      const key = `${route?.subject}:${route?.question_type}`;
      if (keys.has(key)) errors.push(`duplicate capability route: ${key}`);
      keys.add(key);
    });
  }
  return { valid: errors.length === 0, errors };
}

export function resolveCapability({ subject, question_type: questionType, grade_level: gradeLevel }, matrix = loadCapabilityMatrix()) {
  const validation = validateCapabilityMatrix(matrix);
  if (!validation.valid) throw new Error(`Invalid capability matrix: ${validation.errors.join("; ")}`);

  if (!matrix.grade_levels.includes(gradeLevel)) {
    return { ...matrix.default_route, reason: "grade_level_out_of_scope", profile_id: matrix.profile_id };
  }
  // 同题型先匹配学科专属配置，再匹配通配学科；超出范围时使用默认路由。
  const exact = matrix.capabilities.find((route) => route.subject === subject && route.question_type === questionType);
  const wildcard = matrix.capabilities.find((route) => route.subject === "*" && route.question_type === questionType);
  const route = exact ?? wildcard;
  if (!route) return { ...matrix.default_route, reason: "subject_question_type_out_of_scope", profile_id: matrix.profile_id };
  return { ...route, reason: "in_scope", profile_id: matrix.profile_id };
}

export function capabilityRequiresHumanReview(route) {
  return route.review_policy === "always" || route.delivery !== "teacher_suggestion";
}
