import { Fragment, useEffect, useMemo, useRef, useState, useTransition } from "react";
import { App, Button, Input, InputNumber, Pagination, Select, Tag } from "antd";
import { Check, Pencil } from "lucide-react";
import { getUserErrorMessage } from "../../api/client";
import { generatePaperImportRubricDraft, type PaperImportDraftQuestion, type PaperImportJob, type PaperImportSourceRef, type Question, type RubricCandidate, type RubricPayload, type SuggestedRubricCandidate } from "../../api/papers";
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

export function rubricFromSuggestion(candidate: SuggestedRubricCandidate, questionScore: number): RubricPayload {
  return {
    status: "draft",
    max_score: questionScore,
    points: candidate.points.map((point, index) => ({
      id: point.id || `suggested-p${index + 1}`,
      description: point.description,
      score: point.suggested_score ?? 0,
      required: true
    })),
    deductions: [],
    examples: []
  };
}

export function rubricSuggestionContextKey(item: PaperImportDraftQuestion) {
  return JSON.stringify([item.question_no, item.question_type, item.score, item.stem, item.answer_key, item.solution]);
}

function mergeSourceRefs(current: PaperImportSourceRef[], added: PaperImportSourceRef[]) {
  const seen = new Set<string>();
  return [...current, ...added].filter((ref) => { const key = `${ref.source_id}|${ref.file_asset_id}|${ref.document_index}|${ref.page_no ?? ""}|${ref.block_id ?? ""}`; if (seen.has(key)) return false; seen.add(key); return true; });
}

const objectiveQuestionTypes = new Set(["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric"]);
const mathSubjects = new Set(["math", "mathematics", "数学"]);
const subjectiveMathQuestionTypes = new Set(["calculation", "short_answer", "essay", "discussion", "formula"]);
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

export function PaperImportReviewPanel({ job, drafts, readOnly = false, showOverview = true, existingQuestions = [], onChange, onOpenSource }: { job: PaperImportJob; drafts: PaperImportDraftQuestion[]; readOnly?: boolean; showOverview?: boolean; existingQuestions?: Question[]; onChange: (index: number, field: string, patch: Partial<PaperImportDraftQuestion>) => void; onOpenSource: (ref: PaperImportSourceRef) => void }) {
  const { message, modal } = App.useApp();
  const [rawVisible, setRawVisible] = useState<Set<string>>(() => new Set());
  const [suggestions, setSuggestions] = useState<Record<string, SuggestedRubricCandidate>>({});
  const suggestionContexts = useRef<Record<string, string>>({});
  const draftsRef = useRef(drafts);
  draftsRef.current = drafts;
  const [suggestingCandidateId, setSuggestingCandidateId] = useState<string | null>(null);
  // 代次外还比较更新时间，防止同一轮内保存过人工修改后采用旧建议。
  const activeJobKeyRef = useRef(`${job.id}:${job.generation}:${job.updated_at ?? ""}`);
  activeJobKeyRef.current = `${job.id}:${job.generation}:${job.updated_at ?? ""}`;
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

  useEffect(() => {
    setSuggestions({});
    suggestionContexts.current = {};
    setSuggestingCandidateId(null);
  }, [job.id, job.generation, job.updated_at]);

  async function suggestRubric(item: PaperImportDraftQuestion) {
    if (!item.candidate_id) return;
    const jobKey = `${job.id}:${job.generation}:${job.updated_at ?? ""}`;
    const contextKey = rubricSuggestionContextKey(item);
    setSuggestingCandidateId(item.candidate_id);
    try {
      const result = await generatePaperImportRubricDraft(job.id, item.candidate_id, job.generation, job.updated_at ?? "");
      if (activeJobKeyRef.current !== jobKey) return;
      const currentDraft = draftsRef.current.find((draft) => draft.candidate_id === item.candidate_id);
      if (!currentDraft || rubricSuggestionContextKey(currentDraft) !== contextKey) {
        message.warning("题目内容已修改，请先保存人工核对，再重新生成评分点建议");
        return;
      }
      const candidate = result.suggested_rubric_candidates.find((suggestion) => suggestion.candidate_id === item.candidate_id);
      if (!candidate?.points.length) { message.warning("未能根据教师解析生成可用采分点，请人工配置"); return; }
      suggestionContexts.current[item.candidate_id] = contextKey;
      setSuggestions((current) => ({ ...current, [item.candidate_id!]: candidate }));
    } catch (error) {
      if (activeJobKeyRef.current === jobKey) message.error(getUserErrorMessage(error, "生成评分点建议失败，请稍后重试"));
    } finally {
      if (activeJobKeyRef.current === jobKey) setSuggestingCandidateId(null);
    }
  }

  function copySuggestion(item: PaperImportDraftQuestion, index: number, candidate: SuggestedRubricCandidate) {
    if (candidate.points.some((point) => point.suggested_score == null)) {
      message.warning("请先为未标分的采分点填写分值");
      return;
    }
    const jobKey = `${job.id}:${job.generation}:${job.updated_at ?? ""}`;
    const contextKey = rubricSuggestionContextKey(item);
    // 确认弹窗打开期间仍可能修改题目；真正复制时再次检查上下文，不能只在弹窗前检查。
    const copy = () => {
      const currentDraft = draftsRef.current.find((draft) => draft.candidate_id === item.candidate_id);
      if (activeJobKeyRef.current !== jobKey || !currentDraft || rubricSuggestionContextKey(currentDraft) !== contextKey) {
        message.warning("资料或题目已更新，请根据当前结果重新生成评分点建议");
        return;
      }
      onChange(index, "rubric", {
        rubric_candidate_id: undefined,
        rubric: rubricFromSuggestion(candidate, item.score),
        source_refs: mergeSourceRefs(item.source_refs, candidate.points.flatMap((point) => point.source_refs ?? []))
      });
    };
    if (item.rubric?.points.length) {
      modal.confirm({
        title: "替换当前评分细则？",
        content: "复制建议会替换当前题目尚未保存的采分点。复制后仍需逐点核对分值，才能确认导入。",
        okText: "复制建议",
        cancelText: "保留当前内容",
        onOk: copy
      });
      return;
    }
    copy();
  }

  function updateSuggestedScore(candidateId: string, pointId: string, score: number | null) {
    setSuggestions((current) => {
      const candidate = current[candidateId];
      if (!candidate) return current;
      return { ...current, [candidateId]: {
        ...candidate,
        points: candidate.points.map((point) => point.id === pointId ? { ...point, suggested_score: score } : point)
      } };
    });
  }

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
    {showOverview && job.model_usage?.total_tokens ? <div className="paper-import-model-usage" title="Token 数量由模型厂商返回，最终费用以厂商账单为准">
      <strong>{readOnly ? "上一轮模型用量" : "本次模型用量"}</strong>
      <span>输入 {job.model_usage.input_tokens ?? 0}</span>
      <span>输出 {job.model_usage.output_tokens ?? 0}</span>
      {job.model_usage.cached_input_tokens ? <span>缓存命中 {job.model_usage.cached_input_tokens}</span> : null}
      <span>合计 {job.model_usage.total_tokens}</span>
    </div> : null}
    {showOverview && hasMatchResult ? <div className="paper-import-match-summary" role="status">
      <strong>与当前考试比对结果</strong>
      <span>{matchedCount} 道将更新已有题目</span>
      <span>{createdCount} 道将新增</span>
      {conflictCount ? <span className="is-warning">{conflictCount} 道需要确认</span> : null}
      <small>系统优先按标准化题号匹配；不会因为重复粘贴而静默新增同一道题。</small>
    </div> : null}
    {showOverview && !readOnly && unmatchedRubricCandidates.length ? <section className="paper-import-unmatched-rubrics" aria-labelledby="unmatched-rubrics-title">
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
      const options = item.options ?? questionCandidate(item)?.options ?? [];
      const correctOptions = answerOptionLabels(item.answer_key?.standard_answer);
      const match = matchPresentation(item.match_status);
      const matchedQuestion = existingQuestions.find((question) => question.id === item.matched_question_id)
        ?? existingQuestions.find((question) => question.question_no === item.question_no);
      const scoreConflict = Boolean(matchedQuestion && item.score > 0 && matchedQuestion.score !== item.score);
      const typeConflict = Boolean(matchedQuestion && matchedQuestion.question_type !== item.question_type);
      const suggestion = item.candidate_id && suggestionContexts.current[item.candidate_id] === rubricSuggestionContextKey(item) ? suggestions[item.candidate_id] : undefined;
      const savedDraft = (job.questions ?? []).find((draft) => draft.candidate_id === item.candidate_id);
      const suggestionContextDirty = !savedDraft || rubricSuggestionContextKey(savedDraft) !== rubricSuggestionContextKey(item);
      const canSuggestRubric = !readOnly && mathSubjects.has(String(job.subject || "").trim().toLowerCase())
        && subjectiveMathQuestionTypes.has(item.question_type) && Boolean(item.solution?.steps?.length)
        && Boolean(item.solution?.source_refs?.length) && Boolean(item.candidate_id) && Boolean(job.updated_at);
      return <Fragment key={`${item.candidate_id}-${index}`}>
      {showOverview && startsSection ? <h3 className="paper-import-question-type-heading">{sectionNumerals[sectionIndex - 1] ?? sectionIndex}、{questionTypeLabel(item.question_type)}<span>（{questionSectionSummary(drafts, index)}）</span></h3> : null}
      <section className="paper-import-question-review">
      {match ? <div className="paper-import-match-state"><Tag color={match.color}>{match.label}</Tag><span>{match.detail}</span></div> : null}
      {item.score <= 0 || item.score_source === "missing" && !(item.human_confirmed_fields ?? []).includes("score") ? <div className="paper-import-conflict"><strong>分值待确认</strong><span>资料中没有可靠的小题分值；请对照原卷或评分标准填写，不能从题干中的“得分”推定。</span></div> : null}
      {scoreConflict && matchedQuestion && !readOnly ? <div className="paper-import-conflict"><strong>分值冲突：当前 {matchedQuestion.score} 分 → 资料 {item.score} 分</strong><Select aria-label={`第 ${item.question_no} 题分值冲突处理`} placeholder="选择保留哪一项" value={item.score_resolution} options={[{ label: `采用资料 ${item.score} 分`, value: "use_material" }, { label: `保留当前 ${matchedQuestion.score} 分`, value: "use_blueprint" }]} onChange={(value) => onChange(index, "score_resolution", { score_resolution: value })} /></div> : null}
      {typeConflict && matchedQuestion && !readOnly ? <div className="paper-import-conflict"><strong>题型冲突：当前 {questionTypeLabel(matchedQuestion.question_type)} → 资料 {questionTypeLabel(item.question_type)}</strong><Select aria-label={`第 ${item.question_no} 题题型冲突处理`} placeholder="选择保留哪一项" value={item.question_type_resolution} options={[{ label: `采用资料：${questionTypeLabel(item.question_type)}`, value: "use_material" }, { label: `保留当前：${questionTypeLabel(matchedQuestion.question_type)}`, value: "use_blueprint" }]} onChange={(value) => onChange(index, "question_type_resolution", { question_type_resolution: value })} /></div> : null}
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
          {["single_choice", "multiple_choice"].includes(item.question_type) ? <label>选项（每行一项）<Input.TextArea autoSize={{ minRows: 2, maxRows: 8 }} value={options.join("\n")} onChange={(event) => onChange(index, "options", { options: event.target.value.split("\n") })} /></label> : null}
          <label>答案原文<Input.TextArea autoSize={{ minRows: 1, maxRows: 5 }} value={displayPaperImportValue(item.answer_key?.standard_answer)} onChange={(event) => onChange(index, "answer", { answer_key: { standard_answer: event.target.value, equivalent_answers: item.answer_key?.equivalent_answers ?? [], tolerance: item.answer_key?.tolerance ?? {} } })} /></label>
        </div> : null}
      </div>
      <div className="paper-import-field-with-source">
        <div className="paper-import-readable-field"><MathMarkdown>{item.solution?.raw_text ?? ""}</MathMarkdown></div>
        {showRaw ? <label>解析原文（Markdown / LaTeX）<Input.TextArea autoSize={{ minRows: 2, maxRows: 8 }} value={item.solution?.raw_text ?? ""} onChange={(event) => onChange(index, "solution", { solution: { raw_text: event.target.value, steps: item.solution?.steps ?? [], source_refs: item.solution?.source_refs ?? item.source_refs } })} /></label> : null}
      </div>
      {!objectiveQuestionTypes.has(item.question_type) ? <div className="paper-import-rubric-review"><div className="paper-import-rubric-heading"><strong>评分细则</strong>{canSuggestRubric ? <Button size="small" disabled={suggestionContextDirty} title={suggestionContextDirty ? "请先保存人工核对，确保生成建议使用当前题干、答案与解析" : undefined} loading={suggestingCandidateId === item.candidate_id} onClick={() => void suggestRubric(item)}>根据教师解析生成评分点建议</Button> : null}</div>{rubricCandidate(item)?.points.some((point) => point.score == null) ? <div className="text-danger">资料明确包含以下采分点，但未写明分值：{rubricCandidate(item)?.points.filter((point) => point.score == null).map((point) => point.description).join("；")}。请对照教师资料人工填写。</div> : null}{suggestion ? <div className="paper-import-rubric-suggestion"><strong>AI 建议 · 待教师核对</strong><p>以下采分点来自教师解析。模型未明确给出的分值保留为空，须由教师填写后才能复制。</p><ol>{suggestion.points.map((point) => <li key={point.id}><span>{point.description}</span><InputNumber min={0} precision={1} aria-label={`采分点 ${point.description} 的建议分值`} placeholder="待填写" value={point.suggested_score} onChange={(score) => updateSuggestedScore(item.candidate_id!, point.id, score)} /><small>{point.review_note || (point.suggested_score == null ? "资料未给出分值" : "建议分值")}</small></li>)}</ol><Button size="small" type="primary" disabled={suggestion.points.some((point) => point.suggested_score == null)} onClick={() => copySuggestion(item, index, suggestion)}>复制建议到评分细则</Button></div> : null}{readOnly ? <MathMarkdown>{item.rubric?.points.map((point) => `${point.description}${point.score == null ? "" : `（${point.score} 分）`}`).join("\n\n") ?? "暂无评分细则"}</MathMarkdown> : <RubricReviewEditor value={item.rubric} questionScore={item.score} onChange={(rubric) => onChange(index, "rubric", { rubric })} />}<SourceLink label="查看评分标准来源" refs={rubricRefs(item)} onOpen={onOpenSource} /></div> : null}
      {item.issues.length ? <div className="text-danger">当前问题：{item.issues.join("；")}</div> : null}
      </section>
      </Fragment>;
    })}
    </div>
    {pagination}
  </div>;
}
