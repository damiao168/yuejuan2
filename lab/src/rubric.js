import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { validateRubric } from "./schemas/gradingSchema.js";

// 只排序对象键，保留量规点数组顺序；哈希不受对象键写入顺序影响。
function stableSort(value) {
  if (Array.isArray(value)) return value.map(stableSort);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, stableSort(value[key])]));
  }
  return value;
}

export function canonicalRubric(rubric) {
  return JSON.stringify(stableSort(rubric));
}

export function hashRubric(rubric) {
  return createHash("sha256").update(canonicalRubric(rubric)).digest("hex").slice(0, 16);
}

export function validateRubricDsl(rubric) {
  return validateRubric(rubric);
}

export function loadRubricFile(filePath) {
  return JSON.parse(readFileSync(filePath, "utf8"));
}
