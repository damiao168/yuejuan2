import { Fragment, useState } from "react";
import { Button, Input, InputNumber, Select } from "antd";
import { Check, Pencil } from "lucide-react";
import type { PaperImportDraftQuestion, PaperImportJob, PaperImportSourceRef, RubricCandidate, RubricPayload } from "../../api/papers";
import { MathMarkdown } from "../../components/MathMarkdown";
import { questionTypeOptions } from "../../constants/examCatalog";
import { RubricReviewEditor } from "./RubricReviewEditor";

export function displayPaperImportValue(value: unknown) {
  if (typeof value === "string") return value;
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (value == null) return "";
  return JSON.stringify(value, null, 2);
}

function SourceLink({ label, refs, onOpen }: { label: string; refs?: PaperImportSourceRef[]; onOpen: (ref: PaperImportSourceRef) => void }) {
  const ref = refs?.find((item) => item.file_asset_id);
  return ref ? <Button type="link" size="small" onClick={() => onOpen(ref)}>{label}</Button> : null;
}

export function rubricFromCandidate(candidate: RubricCandidate, questionScore: number): RubricPayload {
  return {
    status: "draft",
    max_score: candidate.max_score ?? questionScore,
    points: candidate.points.map((point, index) => ({
      id: point.id || `human-review-p${index + 1}`,
      description: point.description,
      score: point.score ?? 0,
      required: point.required ?? true,
      evidence_requirements: point.evidence_requirements
    })),
    deductions: candidate.deductions,
    examples: candidate.examples
  };
}

function mergeSourceRefs(current: PaperImportSourceRef[], added: PaperImportSourceRef[]) {
  const seen = new Set<string>();
  return [...current, ...added].filter((ref) => { const key = `${ref.source_id}|${ref.file_asset_id}|${ref.document_index}|${ref.page_no ?? ""}|${ref.block_id ?? ""}`; if (seen.has(key)) return false; seen.add(key); return true; });
}

const objectiveQuestionTypes = new Set(["single_choice", "multiple_choice", "true_false"]);
const sectionNumerals = ["一", "二", "三", "四", "五", "六", "七", "八", "九", "十"];

function questionTypeLabel(value: string) {
  return questionTypeOptions.find((option) => option.value === value)?.label ?? "其他题型";
}

function answerOptionLabels(value: unknown, labels = new Set<string>()) {
  if (Array.isArray(value)) {
    value.forEach((item) => answerOptionLabels(item, labels));
    return labels;
  }
  if (value && typeof value === "object") {
    Object.values(value).forEach((item) => answerOptionLabels(item, labels));
    return labels;
  }
  if (typeof value !== "string") return labels;
  const compact = value.toUpperCase().replace(/[^A-Z]/g, "");
  if (/^[A-H]+$/.test(compact)) compact.split("").forEach((label) => labels.add(label));
  return labels;
}

function optionLabel(option: string, index: number) {
  return option.trim().match(/^([A-H])(?:\s|[.．、:：)）])/i)?.[1].toUpperCase() ?? String.fromCharCode(65 + index);
}

function formatScore(value: number) {
  return Number.isInteger(value) ? String(value) : String(Number(value.toFixed(2)));
}

function questionSectionSummary(drafts: PaperImportDraftQuestion[], startIndex: number) {
  const questionType = drafts[startIndex]?.question_type;
  let endIndex = startIndex;
  while (endIndex < drafts.length && drafts[endIndex]?.question_type === questionType) endIndex += 1;
  const sectionDrafts = drafts.slice(startIndex, endIndex);
  const firstScore = sectionDrafts[0]?.score ?? 0;
  const uniformScore = sectionDrafts.every((draft) => draft.score === firstScore);
  const totalScore = sectionDrafts.reduce((total, draft) => total + draft.score, 0);
  return `${uniformScore ? `每题 ${formatScore(firstScore)} 分` : "每题分值不同"}，共 ${sectionDrafts.length} 题，合计 ${formatScore(totalScore)} 分`;
}

export function PaperImportReviewPanel({ job, drafts, onChange, onOpenSource }: { job: PaperImportJob; drafts: PaperImportDraftQuestion[]; onChange: (index: number, field: string, patch: Partial<PaperImportDraftQuestion>) => void; onOpenSource: (ref: PaperImportSourceRef) => void }) {
  const [rawVisible, setRawVisible] = useState<Set<string>>(() => new Set());
  const questionCandidate = (item: PaperImportDraftQuestion) => job.question_candidates.find((candidate) => candidate.candidate_id === item.candidate_id);
  const rubricCandidate = (item: PaperImportDraftQuestion) => job.rubric_candidates?.find((candidate) => candidate.candidate_id === item.rubric_candidate_id);
  const rubricRefs = (item: PaperImportDraftQuestion) => rubricCandidate(item)?.source_refs;
  const assignedRubricCandidateIDs = new Set(drafts.map((item) => item.rubric_candidate_id).filter(Boolean));
  const unmatchedRubricCandidates = (job.rubric_candidates ?? []).filter((candidate) => !assignedRubricCandidateIDs.has(candidate.candidate_id));
  let sectionIndex = 0;
  return <div className="paper-import-review-panel">
    {job.model_usage?.total_tokens ? <div className="paper-import-model-usage" title="Token 数量由模型厂商返回，最终费用以厂商账单为准">
      <strong>本次多模态识别</strong>
      <span>输入 {job.model_usage.input_tokens ?? 0}</span>
      <span>输出 {job.model_usage.output_tokens ?? 0}</span>
      {job.model_usage.cached_input_tokens ? <span>缓存命中 {job.model_usage.cached_input_tokens}</span> : null}
      <span>合计 {job.model_usage.total_tokens}</span>
    </div> : null}
    {unmatchedRubricCandidates.length ? <section className="paper-import-unmatched-rubrics" aria-labelledby="unmatched-rubrics-title">
      <div><strong id="unmatched-rubrics-title">待匹配评分标准</strong><p>这些评分标准没有可靠题号。请选择对应题目，再核对采分点和分值。</p></div>
      {unmatchedRubricCandidates.map((candidate) => <div className="paper-import-unmatched-rubric" key={candidate.candidate_id}>
        <span><strong>{candidate.question_no_hint ? `题号提示：${candidate.question_no_hint}` : "未识别题号"}</strong><small>{candidate.points.length ? candidate.points.map((point) => point.description).filter(Boolean).join("；") : "未提取到采分点，请关联后人工补充"}</small></span>
        <Select
          placeholder="选择关联题目"
          aria-label="选择评分标准对应题目"
          options={drafts.map((draft, index) => ({ label: `第 ${draft.question_no || index + 1} 题 · ${draft.stem.slice(0, 24) || "未填写题干"}`, value: index }))}
          onChange={(index) => {
            const draft = drafts[index];
            if (!draft) return;
            onChange(index, "rubric", { rubric_candidate_id: candidate.candidate_id, rubric: rubricFromCandidate(candidate, draft.score), source_refs: mergeSourceRefs(draft.source_refs, candidate.source_refs) });
          }}
        />
        <SourceLink label="查看评分标准来源" refs={candidate.source_refs} onOpen={onOpenSource} />
      </div>)}
    </section> : null}
    {drafts.map((item, index) => {
      const rawKey = item.candidate_id || `${item.question_no}-${index}`;
      const showRaw = rawVisible.has(rawKey);
      const startsSection = index === 0 || drafts[index - 1]?.question_type !== item.question_type;
      if (startsSection) sectionIndex += 1;
      const options = questionCandidate(item)?.options ?? [];
      const correctOptions = answerOptionLabels(item.answer_key?.standard_answer);
      return <Fragment key={`${item.candidate_id}-${index}`}>
      {startsSection ? <h3 className="paper-import-question-type-heading">{sectionNumerals[sectionIndex - 1] ?? sectionIndex}、{questionTypeLabel(item.question_type)}<span>（{questionSectionSummary(drafts, index)}）</span></h3> : null}
      <section className="paper-import-question-review">
      <div className="paper-import-field-with-source">
        <div className="paper-import-readable-field paper-import-question-content">
          <Button
            className="paper-import-question-edit"
            size="small"
            type="text"
            icon={showRaw ? <Check size={15} /> : <Pencil size={15} />}
            aria-label={showRaw ? "完成编辑" : "编辑题目"}
            title={showRaw ? "完成编辑" : "编辑题目"}
            aria-pressed={showRaw}
            onClick={() => setRawVisible((current) => {
              const next = new Set(current);
              if (next.has(rawKey)) next.delete(rawKey); else next.add(rawKey);
              return next;
            })}
          />
          <MathMarkdown className="paper-import-question-stem">{`**${item.question_no || index + 1}.** ${item.stem}`}</MathMarkdown>
          {options.length ? <div className="paper-import-option-preview">
            {options.map((option, optionIndex) => <div className={`paper-import-option${correctOptions.has(optionLabel(option, optionIndex)) ? " is-correct" : ""}`} key={`${item.candidate_id}-option-${optionIndex}`}><MathMarkdown>{option}</MathMarkdown></div>)}
          </div> : null}
        </div>
        {showRaw ? <div className="paper-import-raw-editor">
          <div className="paper-import-review-grid">
            <label>题号<Input value={item.question_no} onChange={(event) => onChange(index, "question_no", { question_no: event.target.value })} /></label>
            <label>题型<Select value={item.question_type || undefined} options={questionTypeOptions} onChange={(value) => onChange(index, "question_type", { question_type: value })} /></label>
            <label>分值<InputNumber min={0} value={item.score} onChange={(value) => onChange(index, "score", { score: Number(value ?? 0) })} /></label>
          </div>
          <label>题干原文（Markdown / LaTeX）<Input.TextArea autoSize={{ minRows: 2, maxRows: 8 }} value={item.stem} onChange={(event) => onChange(index, "stem", { stem: event.target.value })} /></label>
          <label>答案原文<Input.TextArea autoSize={{ minRows: 1, maxRows: 5 }} value={displayPaperImportValue(item.answer_key?.standard_answer)} onChange={(event) => onChange(index, "answer", { answer_key: { standard_answer: event.target.value, equivalent_answers: item.answer_key?.equivalent_answers ?? [], tolerance: item.answer_key?.tolerance ?? {} } })} /></label>
        </div> : null}
      </div>
      <div className="paper-import-field-with-source">
        <div className="paper-import-readable-field"><MathMarkdown>{item.solution?.raw_text ?? ""}</MathMarkdown></div>
        {showRaw ? <label>解析原文（Markdown / LaTeX）<Input.TextArea autoSize={{ minRows: 2, maxRows: 8 }} value={item.solution?.raw_text ?? ""} onChange={(event) => onChange(index, "solution", { solution: { raw_text: event.target.value, steps: item.solution?.steps ?? [], source_refs: item.solution?.source_refs ?? item.source_refs } })} /></label> : null}
      </div>
      {!objectiveQuestionTypes.has(item.question_type) ? <div><strong>评分细则</strong>{rubricCandidate(item)?.points.some((point) => point.score == null) ? <div className="text-danger">资料明确包含以下采分点，但未写明分值：{rubricCandidate(item)?.points.filter((point) => point.score == null).map((point) => point.description).join("；")}。请对照教师资料人工填写。</div> : null}<RubricReviewEditor value={item.rubric} questionScore={item.score} onChange={(rubric) => onChange(index, "rubric", { rubric })} /><SourceLink label="查看评分标准来源" refs={rubricRefs(item)} onOpen={onOpenSource} /></div> : null}
      {item.issues.length ? <div className="text-danger">当前问题：{item.issues.join("；")}</div> : null}
      </section>
      </Fragment>;
    })}
  </div>;
}
