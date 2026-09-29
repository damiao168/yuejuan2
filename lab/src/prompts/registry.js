import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));

export const PROMPT_REGISTRY = Object.freeze([
  {
    prompt_id: "base_grading",
    prompt_version: "prompt-base-v1",
    question_type: "all",
    file_path: "base_grading_prompt.md",
    created_at: "2026-07-06",
    changelog: "Initial lab baseline prompt."
  },
  {
    prompt_id: "short_answer",
    prompt_version: "prompt-short-answer-v1",
    question_type: "short_answer",
    file_path: "short_answer_prompt.md",
    created_at: "2026-07-06",
    changelog: "Adds concise rubric-evidence scoring for short answers."
  },
  {
    prompt_id: "calculation",
    prompt_version: "prompt-calculation-v2",
    question_type: "calculation",
    file_path: "calculation_prompt.md",
    created_at: "2026-07-06",
    changelog: "Adds explicit contradictory-result rejection alongside step and unit evidence."
  },
  {
    prompt_id: "essay",
    prompt_version: "prompt-essay-v1",
    question_type: "essay",
    file_path: "essay_prompt.md",
    created_at: "2026-07-06",
    changelog: "Adds dimensional rubric and mandatory human review."
  },
  {
    prompt_id: "discussion",
    prompt_version: "prompt-discussion-v1",
    question_type: "discussion",
    file_path: "discussion_prompt.md",
    created_at: "2026-07-06",
    changelog: "Adds claim-evidence reasoning and mandatory human review."
  },
  {
    prompt_id: "local_structured_grading",
    prompt_version: "prompt-local-structured-v2",
    question_type: "local_structured",
    file_path: "local_structured_grading_prompt.md",
    created_at: "2026-07-15",
    changelog: "Adds semantic contradiction checks, strict-alias post-validation, and code-owned feedback."
  },
  {
    prompt_id: "evidence_verification",
    prompt_version: "prompt-evidence-v1",
    question_type: "verification",
    file_path: "evidence_verification_prompt.md",
    created_at: "2026-07-06",
    changelog: "Checks whether each matched point has answer evidence."
  }
]);

export function loadPrompt(promptId) {
  const entry = PROMPT_REGISTRY.find((prompt) => prompt.prompt_id === promptId);
  if (!entry) throw new Error(`Unregistered prompt: ${promptId}`);
  if (!entry.prompt_version) throw new Error(`Prompt ${promptId} is missing prompt_version`);
  const absolutePath = join(here, entry.file_path);
  const text = readFileSync(absolutePath, "utf8");
  // 校验和描述本次读到的文本，用于追溯；此处没有与冻结清单比较来阻止修改。
  const checksum = createHash("sha256").update(text).digest("hex");
  return { ...entry, absolute_path: absolutePath, checksum, text };
}

export function promptForQuestionType(questionType) {
  const entry = PROMPT_REGISTRY.find((prompt) => prompt.question_type === questionType);
  if (!entry) throw new Error(`No prompt registered for question_type: ${questionType}`);
  return loadPrompt(entry.prompt_id);
}

export function listPrompts() {
  return PROMPT_REGISTRY.map((entry) => loadPrompt(entry.prompt_id));
}
