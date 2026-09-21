import { Fragment, useEffect, useMemo, useRef, useState, useTransition } from "react";
import { Button, Input, InputNumber, Pagination, Select, Tag } from "antd";
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

const objectiveQuestionTypes = new Set(["single_choice", "multiple_choice", "true_false", "fill_blank"]);
const sectionNumerals = ["一", "二", "三", "四", "五", "六", "七", "八", "九", "十"];
export const PAPER_REVIEW_PAGE_SIZE = 5;

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
  let sectionStart = startIndex;
  while (sectionStart > 0 && drafts[sectionStart - 1]?.question_type === questionType) sectionStart -= 1;
  let endIndex = sectionStart;
  while (endIndex < drafts.length && drafts[endIndex]?.question_type === questionType) endIndex += 1;
  const sectionDrafts = drafts.slice(sectionStart, endIndex);
  const firstScore = sectionDrafts[0]?.score ?? 0;
  const uniformScore = sectionDrafts.every((draft) => draft.score === firstScore);
  const totalScore = sectionDrafts.reduce((total, draft) => total + draft.score, 0);
  return `${uniformScore ? `每题 ${formatScore(firstScore)} 分` : "每题分值不同"}，共 ${sectionDrafts.length} 题，合计 ${formatScore(totalScore)} 分`;
}

function questionSectionNumber(drafts: PaperImportDraftQuestion[], index: number) {
  let sectionNumber = 0;
  for (let cursor = 0; cursor <= index; cursor += 1) {
    if (cursor === 0 || drafts[cursor - 1]?.question_type !== drafts[cursor]?.question_type) sectionNumber += 1;
  }
  return sectionNumber;
}

function matchPresentation(status: PaperImportDraftQuestion["match_status"]) {
  switch (status) {
    case "matched": return { color: "green", label: "更新已有题目", detail: "题号已匹配，确认后更新原题" };
    case "matched_by_order": return { color: "blue", label: "按顺序匹配", detail: "未可靠识别题号，请重点核对" };
    case "mismatch": return { color: "orange", label: "题号不一致", detail: "与现有题目不一致，请确认题号" };
    case "ambiguous": return { color: "orange", label: "重复题号待确认", detail: "存在多个可能的原题，请人工确认" };
    case "extra": return { color: "blue", label: "新增题目", detail: "超出现有题目范围，将作为新题加入" };
    case "create": return { color: "blue", label: "新增题目", detail: "当前考试中没有同题号题目" };
    default: return undefined;
  }
}

export function PaperImportReviewPanel({ job, drafts, readOnly = false, onChange, onOpenSource }: { job: PaperImportJob; drafts: PaperImportDraftQuestion[]; readOnly?: boolean; onChange: (index: number, field: string, patch: Partial<PaperImportDraftQuestion>) => void; onOpenSource: (ref: PaperImportSourceRef) => void }) {
  const [rawVisible, setRawVisible] = useState<Set<string>>(() => new Set());
  const [currentPage, setCurrentPage] = useState(1);
  const [isPaging, startPaging] = useTransition();
  const reviewStartRef = useRef<HTMLDivElement>(null);
  const questionCandidates = useMemo(() => new Map(job.question_candidates.map((candidate) => [candidate.candidate_id, candidate])), [job.question_candidates]);
  const rubricCandidates = useMemo(() => new Map((job.rubric_candidates ?? []).map((candidate) => [candidate.candidate_id, candidate])), [job.rubric_candidates]);
  const questionCandidate = (item: PaperImportDraftQuestion) => questionCandidates.get(item.candidate_id ?? "");
  const rubricCandidate = (item: PaperImportDraftQuestion) => rubricCandidates.get(item.rubric_candidate_id ?? "");
  const rubricRefs = (item: PaperImportDraftQuestion) => rubricCandidate(item)?.source_refs;
  const assignedRubricCandidateIDs = new Set(drafts.map((item) => item.rubric_candidate_id).filter(Boolean));
  const answerKeyOnlyQuestionNumbers = new Set(drafts.filter((item) => item.question_type === "fill_blank").map((item) => item.question_no));
  const unmatchedRubricCandidates = (job.rubric_candidates ?? []).filter((candidate) =>
    !assignedRubricCandidateIDs.has(candidate.candidate_id)
    && !answerKeyOnlyQuestionNumbers.has(candidate.question_no_normalized ?? "")
  );
  const matchedCount = drafts.filter((item) => item.match_status === "matched" || item.match_status === "matched_by_order").length;
  const createdCount = drafts.filter((item) => item.match_status === "create" || item.match_status === "extra").length;
  const conflictCount = drafts.filter((item) => item.match_status === "mismatch" || item.match_status === "ambiguous").length;
  const hasMatchResult = matchedCount + createdCount + conflictCount > 0;
  const pageCount = Math.max(1, Math.ceil(drafts.length / PAPER_REVIEW_PAGE_SIZE));
  const safePage = Math.min(currentPage, pageCount);
  const pageStart = (safePage - 1) * PAPER_REVIEW_PAGE_SIZE;
  const pageDrafts = drafts.slice(pageStart, pageStart + PAPER_REVIEW_PAGE_SIZE);
  const pageEnd = Math.min(pageStart + pageDrafts.length, drafts.length);

  useEffect(() => {
    if (currentPage > pageCount) setCurrentPage(pageCount);
  }, [currentPage, pageCount]);

  useEffect(() => {
    setCurrentPage(1);
  }, [job.id]);

  const changePage = (page: number) => {
    startPaging(() => setCurrentPage(page));
    requestAnimationFrame(() => reviewStartRef.current?.scrollIntoView({ block: "start" }));
  };

  const pagination = drafts.length > PAPER_REVIEW_PAGE_SIZE ? <nav className="paper-import-review-pagination" aria-label="识别题目分页" aria-busy={isPaging}>
    <span aria-live="polite"><strong>{pageStart + 1}–{pageEnd}</strong> / {drafts.length} 道题</span>
    <Pagination
      current={safePage}
      pageSize={PAPER_REVIEW_PAGE_SIZE}
      total={drafts.length}
      showSizeChanger={false}
      showLessItems
      responsive
      onChange={changePage}
    />
  </nav> : null;

  return <div className="paper-import-review-panel">
    {job.model_usage?.total_tokens ? <div className="paper-import-model-usage" title="Token 数量由模型厂商返回，最终费用以厂商账单为准">
      <strong>{readOnly ? "上一轮模型用量" : "本次模型用量"}</strong>
      <span>输入 {job.model_usage.input_tokens ?? 0}</span>
      <span>输出 {job.model_usage.output_tokens ?? 0}</span>
      {job.model_usage.cached_input_tokens ? <span>缓存命中 {job.model_usage.cached_input_tokens}</span> : null}
      <span>合计 {job.model_usage.total_tokens}</span>
    </div> : null}
    {hasMatchResult ? <div className="paper-import-match-summary" role="status">
      <strong>与当前考试比对结果</strong>
      <span>{matchedCount} 道将更新已有题目</span>
      <span>{createdCount} 道将新增</span>
      {conflictCount ? <span className="is-warning">{conflictCount} 道需要确认</span> : null}
      <small>系统优先按标准化题号匹配；不会因为重复粘贴而静默新增同一道题。</small>
    </div> : null}
    {!readOnly && unmatchedRubricCandidates.length ? <section className="paper-import-unmatched-rubrics" aria-labelledby="unmatched-rubrics-title">
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
    <div className="paper-import-review-page-start" ref={reviewStartRef} />
    {pagination}
    <div className="paper-import-review-page" aria-busy={isPaging}>
    {pageDrafts.map((item, localIndex) => {
      const index = pageStart + localIndex;
      const rawKey = item.candidate_id || `${item.question_no}-${index}`;
      const showRaw = rawVisible.has(rawKey);
      const startsSection = localIndex === 0 || drafts[index - 1]?.question_type !== item.question_type;
      const sectionIndex = questionSectionNumber(drafts, index);
      const options = questionCandidate(item)?.options ?? [];
      const correctOptions = answerOptionLabels(item.answer_key?.standard_answer);
      const match = matchPresentation(item.match_status);
      return <Fragment key={`${item.candidate_id}-${index}`}>
      {startsSection ? <h3 className="paper-import-question-type-heading">{sectionNumerals[sectionIndex - 1] ?? sectionIndex}、{questionTypeLabel(item.question_type)}<span>（{questionSectionSummary(drafts, index)}）</span></h3> : null}
      <section className="paper-import-question-review">
      {match ? <div className="paper-import-match-state"><Tag color={match.color}>{match.label}</Tag><span>{match.detail}</span></div> : null}
      <div className="paper-import-field-with-source">
        <div className="paper-import-readable-field paper-import-question-content">
          {!readOnly ? <Button
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
          /> : null}
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
      {!objectiveQuestionTypes.has(item.question_type) ? <div><strong>评分细则</strong>{rubricCandidate(item)?.points.some((point) => point.score == null) ? <div className="text-danger">资料明确包含以下采分点，但未写明分值：{rubricCandidate(item)?.points.filter((point) => point.score == null).map((point) => point.description).join("；")}。请对照教师资料人工填写。</div> : null}{readOnly ? <MathMarkdown>{item.rubric?.points.map((point) => `${point.description}${point.score == null ? "" : `（${point.score} 分）`}`).join("\n\n") ?? "暂无评分细则"}</MathMarkdown> : <RubricReviewEditor value={item.rubric} questionScore={item.score} onChange={(rubric) => onChange(index, "rubric", { rubric })} />}<SourceLink label="查看评分标准来源" refs={rubricRefs(item)} onOpen={onOpenSource} /></div> : null}
      {item.issues.length ? <div className="text-danger">当前问题：{item.issues.join("；")}</div> : null}
      </section>
      </Fragment>;
    })}
    </div>
    {pagination}
  </div>;
}
