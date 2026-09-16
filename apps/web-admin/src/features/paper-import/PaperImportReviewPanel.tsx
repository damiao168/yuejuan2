import { Button, Input, InputNumber, Select } from "antd";
import type { PaperImportDraftQuestion, PaperImportJob, PaperImportSourceRef, RubricCandidate, RubricPayload } from "../../api/papers";
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

export function PaperImportReviewPanel({ job, drafts, onChange, onOpenSource }: { job: PaperImportJob; drafts: PaperImportDraftQuestion[]; onChange: (index: number, field: string, patch: Partial<PaperImportDraftQuestion>) => void; onOpenSource: (ref: PaperImportSourceRef) => void }) {
  const questionRefs = (item: PaperImportDraftQuestion) => job.question_candidates.find((candidate) => candidate.candidate_id === item.candidate_id)?.source_refs;
  const answerRefs = (item: PaperImportDraftQuestion) => job.answer_candidates.find((candidate) => candidate.candidate_id === item.answer_candidate_id)?.source_refs;
  const solutionRefs = (item: PaperImportDraftQuestion) => job.solution_candidates.find((candidate) => candidate.candidate_id === item.solution_candidate_id)?.source_refs;
  const rubricCandidate = (item: PaperImportDraftQuestion) => job.rubric_candidates?.find((candidate) => candidate.candidate_id === item.rubric_candidate_id);
  const rubricRefs = (item: PaperImportDraftQuestion) => rubricCandidate(item)?.source_refs;
  const assignedRubricCandidateIDs = new Set(drafts.map((item) => item.rubric_candidate_id).filter(Boolean));
  const unmatchedRubricCandidates = (job.rubric_candidates ?? []).filter((candidate) => !assignedRubricCandidateIDs.has(candidate.candidate_id));
  return <div className="paper-import-review-panel">
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
    {drafts.map((item, index) => <section className="paper-import-question-review" key={`${item.candidate_id}-${index}`}>
      <div className="paper-import-review-grid">
        <label>题号<Input value={item.question_no} onChange={(event) => onChange(index, "question_no", { question_no: event.target.value })} /></label>
        <label>题型<Select value={item.question_type || undefined} options={questionTypeOptions} onChange={(value) => onChange(index, "question_type", { question_type: value })} /></label>
        <label>分值<InputNumber min={0} value={item.score} onChange={(value) => onChange(index, "score", { score: Number(value ?? 0) })} /></label>
        <span title="取结构提取、OCR 及公式来源证据的较低参考值；未经逐字准确率校准，不代表整题正确概率">提取证据参考 {Math.round(item.confidence * 100)}%</span>
      </div>
      <div className="paper-import-field-with-source"><label>题干<Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} value={item.stem} onChange={(event) => onChange(index, "stem", { stem: event.target.value })} /></label><SourceLink label="查看题干来源" refs={questionRefs(item)} onOpen={onOpenSource} /></div>
      <div className="paper-import-field-with-source"><label>标准答案<Input.TextArea autoSize={{ minRows: 1, maxRows: 5 }} value={displayPaperImportValue(item.answer_key?.standard_answer)} onChange={(event) => onChange(index, "answer", { answer_key: { standard_answer: event.target.value, equivalent_answers: item.answer_key?.equivalent_answers ?? [], tolerance: item.answer_key?.tolerance ?? {} } })} /></label><SourceLink label="查看答案来源" refs={answerRefs(item)} onOpen={onOpenSource} /></div>
      <div className="paper-import-field-with-source"><label>教师解析<Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} value={item.solution?.raw_text ?? ""} onChange={(event) => onChange(index, "solution", { solution: { raw_text: event.target.value, steps: item.solution?.steps ?? [], source_refs: item.solution?.source_refs ?? item.source_refs } })} /></label><SourceLink label="查看解析来源" refs={solutionRefs(item)} onOpen={onOpenSource} /></div>
      <div><strong>评分细则</strong>{rubricCandidate(item)?.points.some((point) => point.score == null) ? <div className="text-danger">资料明确包含以下采分点，但未写明分值：{rubricCandidate(item)?.points.filter((point) => point.score == null).map((point) => point.description).join("；")}。请对照教师资料人工填写。</div> : null}<RubricReviewEditor value={item.rubric} questionScore={item.score} onChange={(rubric) => onChange(index, "rubric", { rubric })} /><SourceLink label="查看评分标准来源" refs={rubricRefs(item)} onOpen={onOpenSource} /></div>
      {item.issues.length ? <div className="text-danger">当前问题：{item.issues.join("；")}</div> : null}
    </section>)}
  </div>;
}
