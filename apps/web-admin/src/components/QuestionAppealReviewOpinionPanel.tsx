import { Alert, Button, Input, InputNumber, Select } from "antd";
import type { QuestionAppealReviewOpinion, QuestionAppealReviewOpinionRequest } from "../api/questionAppeals";
import { formatQuestionAppealScore } from "./questionAppealPresentation";

export type ReviewOpinionChoice = QuestionAppealReviewOpinionRequest["opinion"];
const choices: Array<{ value: ReviewOpinionChoice; label: string }> = [
  { value: "support_adjustment", label: "支持调整" },
  { value: "do_not_support", label: "不支持调整" },
  { value: "needs_evidence", label: "需补充证据" }
];

export function QuestionAppealReviewOpinionPanel({ saved, pending, canSubmit, opinion, suggestedScore, explanation, maxScore, busy, onOpinionChange, onScoreChange, onExplanationChange, onSubmit }: Readonly<{
  saved?: QuestionAppealReviewOpinion; pending: boolean; canSubmit: boolean; opinion: ReviewOpinionChoice | ""; suggestedScore?: number; explanation: string; maxScore: number; busy: boolean;
  onOpinionChange: (value: ReviewOpinionChoice) => void; onScoreChange: (value: number | undefined) => void; onExplanationChange: (value: string) => void; onSubmit: () => void;
}>) {
  if (!saved && !canSubmit) return null;
  return <section className="question-appeal-actions" aria-labelledby="question-appeal-review-opinion-title">
    <h3 id="question-appeal-review-opinion-title">独立复核意见</h3>
    {saved ? <Alert type="info" showIcon message={`${pending ? "已移交学校管理员" : "已记录的独立复核意见"}：${choices.find((item) => item.value === saved.opinion)?.label ?? "复核意见"}`} description={<>
      <p>{saved.explanation}</p>{saved.suggested_score !== undefined ? <p>建议本题得分：{formatQuestionAppealScore(saved.suggested_score)} / {formatQuestionAppealScore(maxScore)}</p> : null}
      <p>{pending ? "意见已移交学校管理员，当前成绩未改变。" : "此处保留教师建议；处理结果以管理员答复和正式成绩为准。"}</p>
    </>} /> : null}
    {canSubmit ? <>
      <p>{saved ? "修改后需再次提交；意见和建议分仅供教职工查看。" : "核对原公布材料后提交独立意见。意见和建议分仅供教职工查看，不直接改分。"}</p>
      <label htmlFor="question-appeal-review-opinion">复核意见（必选）</label>
      <Select id="question-appeal-review-opinion" aria-label="复核意见" value={opinion || undefined} options={choices} onChange={onOpinionChange} disabled={busy} placeholder="请选择复核意见" />
      <label htmlFor="question-appeal-suggested-score">建议本题得分（可选，0–{formatQuestionAppealScore(maxScore)} 分）</label>
      <InputNumber id="question-appeal-suggested-score" aria-label="建议本题得分" min={0} max={maxScore} value={suggestedScore ?? null} onChange={(value) => onScoreChange(value ?? undefined)} disabled={busy} />
      <label htmlFor="question-appeal-review-explanation">依据与说明（必填）</label>
      <Input.TextArea id="question-appeal-review-explanation" rows={4} maxLength={4000} value={explanation} onChange={(event) => onExplanationChange(event.target.value)} disabled={busy} placeholder="写明核对的证据、评分依据；需要补证时说明缺少什么。" />
      <Button type="primary" loading={busy} disabled={busy || !opinion || !explanation.trim()} onClick={onSubmit}>保存意见并移交学校管理员</Button>
    </> : null}
  </section>;
}
