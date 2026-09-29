import { createHash } from "node:crypto";

export const JORGPT_SOURCE_ID = "jorgpt_zenodo_18981627_en";
export const JORGPT_MAX_SCORE = 10;
export const JORGPT_RAW_COLUMNS = Object.freeze([
  "entry_id",
  "question_id",
  "question_text",
  "student_answer",
  "ideal_answer",
  "student_anom_id",
  "deepseek_raw_json",
  "deepseek_grade",
  "qwen_raw_json",
  "qwen_grade",
  "gemini_raw_json",
  "gemini_grade",
  "judge_status",
  "judge_raw_json",
  "judge_grade",
  "student_answer_length",
  "teacher_grade",
  "teacher_feedback",
  "teacher_corrected_at",
  "teacher_corrected"
]);

export const JORGPT_DROPPED_COLUMNS = Object.freeze([
  "student_anom_id",
  "deepseek_raw_json",
  "deepseek_grade",
  "qwen_raw_json",
  "qwen_grade",
  "gemini_raw_json",
  "gemini_grade",
  "judge_status",
  "judge_raw_json",
  "judge_grade",
  "student_answer_length",
  "teacher_corrected_at",
  "teacher_corrected"
]);

export function redactStructuredIdentityValues(value) {
  let redactions = 0;
  const text = String(value ?? "").replace(
    /((?:^|[,{])\s*["']?(?:name|surname|first_name|last_name)["']?\s*:\s*)([^,}\r\n]{1,80})(?=\s*[,}])/giu,
    (_match, prefix) => {
      redactions += 1;
      return `${prefix}[redacted]`;
    }
  );
  return { text, redactions };
}

function nonEmpty(value, field, rowIndex) {
  if (typeof value !== "string" || !value.trim()) throw new Error(`row ${rowIndex}.${field} must be non-empty`);
  return value.trim();
}

function teacherGrade(value, rowIndex) {
  const grade = Number(value);
  if (!Number.isInteger(grade) || grade < 0 || grade > JORGPT_MAX_SCORE) {
    throw new Error(`row ${rowIndex}.teacher_grade must be an integer from 0 to ${JORGPT_MAX_SCORE}`);
  }
  return grade;
}

function participantGroupId(rawId) {
  return createHash("sha256").update(`edugrade-jorgpt-participant-v1\0${rawId}`).digest("hex");
}

// 量规由公开理想答案重建，仅用于外部基准，不能冒充原教师逐点量规或双评 Gold。
function rubricFor(questionId, idealAnswer) {
  return {
    rubric_id: `jorgpt-${questionId}-derived-holistic`,
    rubric_version: `jorgpt-${questionId}-derived-holistic-v1`,
    question_type: "short_answer",
    max_score: JORGPT_MAX_SCORE,
    points: [
      {
        id: "holistic_correctness",
        description: "Correctness and completeness relative to the instructor ideal answer.",
        score: JORGPT_MAX_SCORE,
        required: true,
        aliases: [idealAnswer],
        evidence_required: true,
        match_policy: "semantic"
      }
    ],
    deductions: [],
    equivalent_answers: [],
    examples: [],
    scoring_notes: [
      "External benchmark rubric reconstructed from the published ideal answer.",
      "Partial scores are permitted, but every awarded score requires answer-local evidence."
    ]
  };
}

export function validateJorgptHeaders(headers) {
  const actual = Array.isArray(headers) ? headers : [];
  const missing = JORGPT_RAW_COLUMNS.filter((column) => !actual.includes(column));
  const unexpected = actual.filter((column) => !JORGPT_RAW_COLUMNS.includes(column));
  return { valid: missing.length === 0 && unexpected.length === 0, missing, unexpected };
}

export function transformJorgptRows(rows) {
  if (!Array.isArray(rows) || rows.length === 0) throw new Error("JorGPT rows must be a non-empty array");
  const entryIds = new Set();
  const questionSignatures = new Map();

  return rows.map((row, index) => {
    const rowIndex = index + 2;
    const entryId = nonEmpty(row.entry_id, "entry_id", rowIndex);
    if (entryIds.has(entryId)) throw new Error(`duplicate JorGPT entry_id: ${entryId}`);
    entryIds.add(entryId);

    const sourceQuestionId = nonEmpty(row.question_id, "question_id", rowIndex);
    const questionTextRaw = nonEmpty(row.question_text, "question_text", rowIndex);
    const idealAnswerRaw = nonEmpty(row.ideal_answer, "ideal_answer", rowIndex);
    const rawParticipantId = nonEmpty(row.student_anom_id, "student_anom_id", rowIndex);
    const rationaleRaw = nonEmpty(row.teacher_feedback, "teacher_feedback", rowIndex);
    if (String(row.teacher_corrected).toLowerCase() !== "true") {
      throw new Error(`row ${rowIndex}.teacher_corrected must be true`);
    }
    if (typeof row.student_answer !== "string") throw new Error(`row ${rowIndex}.student_answer must be a string`);

    const questionRedaction = redactStructuredIdentityValues(questionTextRaw);
    const idealRedaction = redactStructuredIdentityValues(idealAnswerRaw);
    const answerRedaction = redactStructuredIdentityValues(row.student_answer);
    const rationaleRedaction = redactStructuredIdentityValues(rationaleRaw);
    const questionText = questionRedaction.text;
    const idealAnswer = idealRedaction.text;
    const rationale = rationaleRedaction.text;
    const privacyRedactionCount = questionRedaction.redactions + idealRedaction.redactions +
      answerRedaction.redactions + rationaleRedaction.redactions;

    const signature = createHash("sha256").update(`${questionText}\0${idealAnswer}`).digest("hex");
    const previousSignature = questionSignatures.get(sourceQuestionId);
    if (previousSignature && previousSignature !== signature) {
      throw new Error(`question ${sourceQuestionId} has inconsistent text or ideal answer`);
    }
    questionSignatures.set(sourceQuestionId, signature);

    const score = teacherGrade(row.teacher_grade, rowIndex);
    const questionId = `jorgpt-${sourceQuestionId}`;
    const rubric = rubricFor(sourceQuestionId, idealAnswer);
    return {
      sample_id: `jorgpt-en-${entryId.padStart(4, "0")}`,
      synthetic: false,
      privacy_status: "anonymized",
      source_id: JORGPT_SOURCE_ID,
      source_record_id: entryId,
      participant_group_id: participantGroupId(rawParticipantId),
      question_id: questionId,
      answer_segment_id: `jorgpt-en-answer-${entryId.padStart(4, "0")}`,
      subject: "computer_science",
      grade_level: "higher_education",
      question_type: "short_answer",
      question_text: questionText,
      max_score: JORGPT_MAX_SCORE,
      rubric,
      rubric_version: rubric.rubric_version,
      rubric_provenance: "derived_from_published_ideal_answer_not_original_teacher_rubric",
      answer_text: answerRedaction.text,
      ocr_confidence: 1,
      expected_score: score,
      expected_matched_points: score > 0 ? ["holistic_correctness"] : [],
      expected_missing_points: score === 0 ? ["holistic_correctness"] : [],
      human_rationale: rationale,
      human_label_type: "single_teacher_holistic_grade",
      privacy_redaction_count: privacyRedactionCount,
      should_need_human_review: true,
      model_policy: { final_score_allowed: false },
      training_eligible: false,
      tags: ["external_real", "jorgpt", "english_translation", "benchmark_only"]
    };
  });
}

// 参与者分组哈希只用于拆分泄漏审计；持久化前删除，减少可关联信息。
export function minimizeJorgptSamplesForPersistence(samples) {
  return samples.map(({ participant_group_id: _participantGroupId, ...sample }) => sample);
}

export function participantLeakageAudit(split) {
  const owners = new Map();
  const leaked = new Set();
  for (const [partition, samples] of Object.entries(split)) {
    for (const sample of samples) {
      const participant = sample.participant_group_id;
      if (!participant) continue;
      if (!owners.has(participant)) owners.set(participant, new Set());
      owners.get(participant).add(partition);
      if (owners.get(participant).size > 1) leaked.add(participant);
    }
  }
  return {
    passed: leaked.size === 0,
    unique_participants: owners.size,
    participants_across_multiple_partitions: leaked.size
  };
}
