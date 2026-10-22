import { useState } from "react";
import { Alert, Button, Pagination } from "antd";
import type { PaperImportDraftQuestion, PaperImportJob, PaperImportSourceRef } from "../../api/papers";
import { MathMarkdown } from "../../components/MathMarkdown";
import { questionTypeOptions } from "../../constants/examCatalog";

const PREVIEW_PAGE_SIZE = 5;
type OpenSource = (fileAssetID: string, pageNo?: number) => void;

function scoringPointText(point: { description: string; score?: number | null }) {
  const score = point.score == null ? "分值待确认" : `${point.score} 分`;
  return `${point.description}（${score}）`;
}

function answerText(value: unknown) {
  if (value == null || typeof value === "string" && !value.trim()) return "未识别到答案";
  if (typeof value === "object") return "```json\n" + JSON.stringify(value, null, 2) + "\n```";
  return String(value);
}

function RecognitionSource({ refs, job, onOpenSource }: Readonly<{ refs?: PaperImportSourceRef[]; job: PaperImportJob; onOpenSource: OpenSource }>) {
  const source = refs?.find((item) => item.file_asset_id);
  if (!source) return null;
  const name = job.sources.find((item) => item.file_asset_id === source.file_asset_id)?.original_name;
  return <Button type="link" size="small" onClick={() => onOpenSource(source.file_asset_id, source.page_no)}>
    查看来源{name ? ` · ${name}` : ""}{source.page_no ? ` · 第 ${source.page_no} 页` : ""}
  </Button>;
}

function RecognizedQuestion({ item, index, job, onOpenSource }: Readonly<{
  item: PaperImportDraftQuestion; index: number; job: PaperImportJob; onOpenSource: OpenSource;
}>) {
  const questionCandidate = job.question_candidates?.find((candidate) => candidate.candidate_id === item.candidate_id);
  const answerCandidate = job.answer_candidates?.find((candidate) => candidate.candidate_id === item.answer_candidate_id);
  const rubricCandidate = job.rubric_candidates?.find((candidate) => candidate.candidate_id === item.rubric_candidate_id);
  const options = item.options ?? questionCandidate?.options ?? [];
  const typeLabel = questionTypeOptions.find((option) => option.value === item.question_type)?.label ?? "题型待核对";
  const missingScore = item.score <= 0 || item.score_source === "missing" && !(item.human_confirmed_fields ?? []).includes("score");
  const solution = item.solution?.raw_text || item.solution?.steps.map((step) => step.content).join("\n\n");
  const points = item.rubric?.points ?? rubricCandidate?.points ?? [];
  return <article className="paper-import-question-review" aria-label={`第 ${item.question_no || index + 1} 题识别结果`}>
    <div className="paper-import-facts"><strong>第 {item.question_no || index + 1} 题</strong><span>{typeLabel}</span><span>{missingScore ? "分值待确认" : `${item.score} 分`}</span></div>
    <div className="paper-import-readable-field">
      <span>题干与选项</span>
      <MathMarkdown promoteInlineMath={false}>{item.stem || questionCandidate?.stem || "未识别到题干"}</MathMarkdown>
      {options.length ? <div className="paper-import-option-preview">{options.map((option, optionIndex) => <MathMarkdown key={`${item.candidate_id}-option-${optionIndex}`} promoteInlineMath={false}>{option}</MathMarkdown>)}</div> : null}
      <RecognitionSource job={job} refs={questionCandidate?.source_refs.length ? questionCandidate.source_refs : item.source_refs} onOpenSource={onOpenSource} />
    </div>
    <div className="paper-import-readable-field">
      <span>识别答案</span><MathMarkdown promoteInlineMath={false}>{answerText(item.answer_key?.standard_answer)}</MathMarkdown>
      {item.answer_key?.equivalent_answers.length ? <details><summary>等价答案</summary><MathMarkdown promoteInlineMath={false}>{answerText(item.answer_key.equivalent_answers)}</MathMarkdown></details> : null}
      <RecognitionSource job={job} refs={answerCandidate?.source_refs} onOpenSource={onOpenSource} />
    </div>
    <div className="paper-import-readable-field">
      <span>识别解析</span><MathMarkdown promoteInlineMath={false}>{solution || "未识别到解析"}</MathMarkdown>
      <RecognitionSource job={job} refs={item.solution?.source_refs} onOpenSource={onOpenSource} />
    </div>
    {item.rubric || rubricCandidate ? <div className="paper-import-readable-field">
      <span>识别评分标准</span>
      <span>标准满分：{item.rubric?.max_score ?? rubricCandidate?.max_score ?? "待确认"}</span>
      <MathMarkdown promoteInlineMath={false}>{points.length ? points.map(scoringPointText).join("\n\n") : "未识别到评分点"}</MathMarkdown>
      <RecognitionSource job={job} refs={rubricCandidate?.source_refs} onOpenSource={onOpenSource} />
    </div> : null}
    {item.issues?.length ? <div className="text-danger">识别时提示：{item.issues.join("；")}</div> : null}
  </article>;
}

function UnmatchedRecognitionPreview({ job, onOpenSource }: Readonly<{ job: PaperImportJob; onOpenSource: OpenSource }>) {
  // 没有合并草稿时直接展示候选内容；预览不能自行按题号配对并伪装成已确认题目。
  const candidates = [
    ...(job.question_candidates ?? []).map((item) => ({ key: `question-${item.candidate_id}`, label: `题目 ${item.question_no_normalized || item.question_no_raw || "（题号待核对）"}`, text: [item.stem || "未识别到题干", ...item.options, item.score == null ? "分值待确认" : `${item.score} 分`].join("\n\n"), refs: item.source_refs })),
    ...(job.answer_candidates ?? []).map((item) => ({ key: `answer-${item.candidate_id}`, label: `答案 ${item.question_no_normalized || item.question_no_hint || "（题号待核对）"}`, text: answerText(item.standard_answer), refs: item.source_refs })),
    ...(job.solution_candidates ?? []).map((item) => ({ key: `solution-${item.candidate_id}`, label: `解析 ${item.question_no_normalized || item.question_no_hint || "（题号待核对）"}`, text: item.raw_text || item.steps.map((step) => step.content).join("\n\n") || "未识别到解析正文", refs: item.source_refs })),
    ...(job.rubric_candidates ?? []).map((item) => ({ key: `rubric-${item.candidate_id}`, label: `评分标准 ${item.question_no_normalized || item.question_no_hint || "（题号待核对）"}`, text: item.points.map(scoringPointText).join("\n\n") || "未识别到评分点", refs: item.source_refs }))
  ];
  if (!candidates.length) return <Alert type="warning" showIcon message="暂无可展示的识别结果" description="当前任务未返回题目、答案、解析或评分标准。请查看上方资料来源，或到小题与分值核对现有配置。" />;
  return <div className="paper-import-review-panel">
    <Alert type="info" showIcon message="以下为尚未形成题目草稿的识别内容" description="题号和内容关联仍需人工核对，当前仅展示资料提取结果。" />
    {candidates.map((item) => <article className="paper-import-readable-field" key={item.key}><strong>{item.label}</strong><MathMarkdown promoteInlineMath={false}>{item.text}</MathMarkdown><RecognitionSource job={job} refs={item.refs} onOpenSource={onOpenSource} /></article>)}
  </div>;
}

export function PaperImportRecognitionPreview({ job, onOpenSource }: Readonly<{ job: PaperImportJob; onOpenSource: OpenSource }>) {
  const [currentPage, setCurrentPage] = useState(1);
  // 采用结构不会清除识别结果；此处展示接口保存的快照，不将本地未保存草稿当成采用回执。
  const questions = job.questions;
  if (!questions.length) return <UnmatchedRecognitionPreview job={job} onOpenSource={onOpenSource} />;
  const pageCount = Math.ceil(questions.length / PREVIEW_PAGE_SIZE);
  const safePage = Math.min(currentPage, pageCount);
  const startIndex = (safePage - 1) * PREVIEW_PAGE_SIZE;
  const visibleQuestions = questions.slice(startIndex, startIndex + PREVIEW_PAGE_SIZE);
  return <div className="paper-import-review-panel">
    <div className="paper-import-facts"><span>识别题目 <strong>{questions.length}</strong> 道</span><span>识别分值合计 <strong>{questions.reduce((total, item) => total + item.score, 0)}</strong> 分</span></div>
    <p className="muted">以下为本轮识别快照，供对照资料；当前考试的答案、分值和正式评分配置以小题与分值页面为准。</p>
    <div className="paper-import-review-page">{visibleQuestions.map((item, index) => <RecognizedQuestion key={`${item.candidate_id ?? item.question_no}-${startIndex + index}`} item={item} index={startIndex + index} job={job} onOpenSource={onOpenSource} />)}</div>
    {questions.length > PREVIEW_PAGE_SIZE ? <nav className="paper-import-review-pagination" aria-label="识别结果分页">
      <span>{startIndex + 1}–{startIndex + visibleQuestions.length} / {questions.length} 道题</span>
      <Pagination current={safePage} pageSize={PREVIEW_PAGE_SIZE} total={questions.length} showSizeChanger={false} showLessItems responsive onChange={setCurrentPage} />
    </nav> : null}
  </div>;
}
