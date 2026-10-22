import { useEffect, useState } from "react";
import { Alert, Spin, Tabs } from "antd";
import { getBusinessCommandSession } from "../../../../api/businessCommand";
import { listAiGrades, type ScoringRunItem } from "../../../../api/review";
import { MathMarkdown } from "../../../../components/MathMarkdown";
import { formatAnswer, formatError } from "../gradingWorkbench.model";
import { gradingAIModeLabels } from "./GradingAIPlanSettings";

type RecordValue = Record<string, unknown>;
export interface ImageGradeRecordData {
  question_id: string;
  suggested_score: number;
  max_score: number;
  model_version?: string;
  raw_output?: RecordValue;
}
function record(value: unknown): RecordValue { return value && typeof value === "object" && !Array.isArray(value) ? value as RecordValue : {}; }
function records(value: unknown): RecordValue[] { return Array.isArray(value) ? value.map(record) : []; }
function text(value: unknown): string { return typeof value === "string" ? value : ""; }
function score(value: unknown): number | undefined { return typeof value === "number" && Number.isFinite(value) ? value : undefined; }
const roleLabels: Record<string, string> = { single: "评分模型", primary_a: "评分 A", primary_b: "评分 B", arbiter: "第三评分" };
const reasonLabels: Record<string, string> = {
  primary_difference_exceeded: "前两模型分差达到阈值", primary_diff_exceeded: "前两模型分差达到阈值",
  arbiter_unavailable: "未配置可用的第三模型", arbiter_difference_exceeded: "第三评分与前两模型均分差异过大",
  panel_range_exceeded: "三个模型的最高与最低分差异过大", role_failed: "有模型未成功返回评分",
  student_answer_uncertain: "作答内容无法可靠判断", single_model_only: "当前只有一个独立模型",
  primary_a_failed: "评分 A 未成功返回", primary_b_failed: "评分 B 未成功返回", arbiter_failed: "第三评分未成功返回", single_failed: "评分模型未成功返回",
  criterion_evidence_uncertain: "有采分点证据无法确定", student_observation_unreadable: "学生作答无法可靠辨认或题目对应不明",
  arbiter_difference_exceeds_teacher_threshold: "三个模型仍有较大分歧，达到教师处理阈值",
  arbiter_unavailable_for_difference: "前两模型分差达到阈值，但没有可用的第三模型"
};

export function imagePanelHasNoFinalScore(raw: RecordValue | undefined): boolean {
  const panel = record(raw?.panel_execution);
  return Object.keys(panel).length > 0 && score(panel.final_score) === undefined;
}

function JudgmentRecord({ raw, questionId, basis }: Readonly<{ raw: RecordValue; questionId: string; basis: RecordValue }>) {
  const members = records(record(raw.observation).members);
  const member = members.find(value => value.question_id === questionId);
  const answer = record(member?.answer);
  const answerValue = text(answer.value) || (Array.isArray(answer.parts) ? answer.parts.filter(value => typeof value === "string").join("\n") : "");
  const points = records(basis.rubric_points);
  const judgments = records(raw.judgments);
  const evidence = records(member?.evidence);
  return <div className="grading-record-body">
    <div><h3>模型识别的作答</h3>{answerValue ? <MathMarkdown>{answerValue}</MathMarkdown> : <p>{member?.presence === "blank" ? "识别为空白作答" : "未保留可展示的转写，请查看原图。"}</p>}</div>
    <div><h3>逐项评分依据</h3>
      {judgments.length ? <div className="grading-record-points">{judgments.map((judgment, index) => {
        const point = points.find(value => value.id === judgment.criterion_id);
        const supported = judgment.status === "supported";
        const evidenceIDs = Array.isArray(judgment.evidence_ids) ? judgment.evidence_ids : [];
        const snippets = evidence.filter(value => evidenceIDs.includes(value.evidence_id));
        return <article key={text(judgment.criterion_id) || index}>
          <header><strong>{text(point?.description) || `采分点 ${index + 1}`}</strong><span>{supported ? `命中${score(point?.score) === undefined ? "" : ` · ${point?.score} 分`}` : judgment.status === "uncertain" ? "无法确定" : "未命中"}</span></header>
          <MathMarkdown>{text(judgment.reason) || "未保留具体理由"}</MathMarkdown>
          {snippets.length ? <div className="grading-record-quotes">{snippets.map((snippet, position) => <blockquote key={text(snippet.evidence_id) || position}><MathMarkdown>{text(snippet.text)}</MathMarkdown></blockquote>)}</div> : null}
        </article>;
      })}</div> : <p>这条历史记录未保留逐项判断。</p>}
    </div>
  </div>;
}

// 只展示已保存的转写、采分点理由和图像证据；不读取供应商的私有思维链字段。
export function ImageGradingRecord({ grade }: Readonly<{ grade: ImageGradeRecordData }>) {
  const raw = record(grade.raw_output);
  const panel = record(raw.panel_execution);
  const basis = record(raw.scoring_basis);
  const roles = records(panel.role_results);
  const mode = text(panel.configured_mode) || "single";
  const finalScore = score(panel.final_score);
  const noFinal = imagePanelHasNoFinalScore(raw);
  const reasons = Array.isArray(panel.reason_codes) ? panel.reason_codes.filter(value => typeof value === "string") as string[] : [];
  const successful = roles.filter(role => role.status === "succeeded");
  return <section className="grading-ai-record" aria-label="AI 评分记录">
    <h2>AI 评分记录</h2>
    <div className="grading-record-heading"><strong>{gradingAIModeLabels[mode] ?? "AI 评分"}</strong><span>{noFinal ? "未汇总，需教师处理" : `建议分 ${finalScore ?? grade.suggested_score} / ${grade.max_score}`}</span></div>
    {roles.length ? <p>{roles.map(role => `${roleLabels[text(role.role)] || "评分模型"}：${text(record(role.model).display_name) || text(record(role.model).model_name) || "已配置模型"} ${role.status === "succeeded" ? `${score(role.score) ?? "—"} 分` : "评分失败"}`).join("；")}</p> : <p>使用模型：{grade.model_version || text(raw.model_version) || "历史记录未保留模型名称"}</p>}
    {panel.decision === "primary_consensus" ? <p>前两模型分差未超过允许的 {score(panel.primary_threshold) ?? "—"} 分，未调用第三模型。取 A、B 均分后向上取整。</p> : null}
    {panel.decision === "arbiter_weighted" || (panel.decision === "teacher_review" && roles.some(role => role.role === "arbiter")) ? <p>已调用第三模型，汇总权重为 A 25%、B 25%、第三模型 50%。{score(panel.raw_score) !== undefined ? `取整前 ${panel.raw_score} 分。` : ""}</p> : null}
    {noFinal || panel.decision === "teacher_review" ? <Alert type="warning" showIcon message={noFinal ? "本次未形成汇总分，请教师评分" : "模型分歧较大，请教师核对"} description={reasons.length ? reasons.map(reason => reasonLabels[reason] || "评分未能可靠汇总，请查看各模型记录").join("；") : "请结合原图、标准答案和各模型依据判断。"} /> : null}
    <div><h3>本次冻结的标准答案</h3>{basis.standard_answer !== undefined && basis.standard_answer !== null ? <MathMarkdown>{formatAnswer(basis.standard_answer)}</MathMarkdown> : <p>历史记录未保留标准答案。</p>}</div>
    {text(basis.solution_text) ? <details><summary>标准解答</summary><MathMarkdown>{text(basis.solution_text)}</MathMarkdown></details> : null}
    {successful.length ? <Tabs size="small" items={successful.map((role, index) => ({ key: text(role.role) || String(index), label: `${roleLabels[text(role.role)] || "评分记录"} · ${score(role.score) ?? "—"} 分`, children: <JudgmentRecord raw={record(role.raw_output)} questionId={grade.question_id} basis={basis} /> }))} /> : <JudgmentRecord raw={raw} questionId={grade.question_id} basis={basis} />}
  </section>;
}

export function ImageGradingRecordLoader({ item }: Readonly<{ item: ScoringRunItem }>) {
  const [grade, setGrade] = useState<ImageGradeRecordData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (item.route !== "image_ai" && !item.input_version_id) return;
    let disposed = false;
    const session = getBusinessCommandSession();
    setGrade(null); setError(""); setLoading(true);
    void listAiGrades(item.answer_segment_id).then(result => {
      if (disposed || session !== getBusinessCommandSession()) return;
      // 同一片段可能保留旧图像和旧量规建议；只展示当前结果绑定，不能拿最新一条凑数。
      setGrade(result.grades.find(value => value.question_id === item.question_id && value.raw_output?.schema_version === "grading-image-v3"
        && value.raw_output?.input_version_id === item.input_version_id
        && (!item.assessment_snapshot_id || value.raw_output?.assessment_snapshot_id === item.assessment_snapshot_id)) ?? null);
    }).catch(reason => { if (!disposed && session === getBusinessCommandSession()) setError(formatError(reason)); })
      .finally(() => { if (!disposed && session === getBusinessCommandSession()) setLoading(false); });
    return () => { disposed = true; };
  }, [item.answer_segment_id, item.question_id, item.input_version_id, item.assessment_snapshot_id, item.route]);
  if (item.route !== "image_ai" && !item.input_version_id) return null;
  return loading ? <Spin /> : error ? <Alert type="warning" message="评分依据暂未读到" description={error} /> : grade ? <ImageGradingRecord grade={grade} /> : <p>当前作答尚无对应的 AI 评分记录。</p>;
}
