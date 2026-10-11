import type { StudentQuestion, StudentResult } from "../../api";

const subjectNames: Record<string, string> = {
  math: "数学", mathematics: "数学", chinese: "语文", english: "英语", physics: "物理",
  chemistry: "化学", biology: "生物", history: "历史", geography: "地理", politics: "政治"
};

export function displaySubject(subject?: string) {
  if (!subject) return "考试";
  return subjectNames[subject.toLowerCase()] ?? subject;
}

export function ScoreHero({ result }: Readonly<{ result: StudentResult }>) {
  const rate = Math.round((result.score_rate ?? (result.max_score > 0 ? result.total_score / result.max_score : 0)) * 1000) / 10;
  const overallScore = result.overall_total_score ?? result.total_score;
  const overallMax = result.overall_max_score ?? result.max_score;
  const session = result.session_summary;
  const multi = Boolean(session && session.expected_count > 1);
  return <section className="score-hero" aria-label="总分">
    <Metric label={`${displaySubject(result.exam?.subject)}成绩`} value={`${formatScore(result.total_score)} / ${formatScore(result.max_score)}`} note={`得分率 ${rate}%`} />
    {multi ? <Metric label={session!.published_count === session!.expected_count ? "本次考试总分" : `已发布科目合计（${session!.published_count}/${session!.expected_count}科）`} value={`${formatScore(overallScore)} / ${formatScore(overallMax)}`} /> : null}
    <Metric label="班级排名" value={rank(result.rankings?.class_rank, result.rankings?.class_size)} note={`本学科本次考试 · ${rankReason(result.rankings?.class_unavailable_reason, result.rankings?.class_rank)}`} />
    <Metric label="年级排名" value={rank(result.rankings?.grade_rank, result.rankings?.grade_size)} note={`本学科本次考试 · ${rankReason(result.rankings?.grade_unavailable_reason, result.rankings?.grade_rank)}`} />
    {multi ? <details className="session-subjects"><summary>查看纳入科目与发布时间</summary><p>纳入范围来自本人本考次已冻结的应考名单；未发布科目不计入已发布合计。</p><ul>{session!.subjects.map((item) => <li key={item.exam_id}>{displaySubject(item.subject)}：{publishedSubjectScoreText(item)}</li>)}</ul></details> : null}
  </section>;
}

function publishedSubjectScoreText(item: NonNullable<StudentResult["session_summary"]>["subjects"][number]) {
  // 已发布且分数齐全才显示日期；0分不是缺失，未公开科目不能补成0分。
  if (item.status !== "published" || item.score === undefined || item.max_score === undefined) return "等待学校发布";
  const scores = `${formatScore(item.score)} / ${formatScore(item.max_score)}`;
  const publishedAt = item.published_at ? new Date(item.published_at).toLocaleString("zh-CN") : "时间未提供";
  return `${scores}，发布于 ${publishedAt}`;
}

function Metric({ label, value, note }: Readonly<{ label: string; value: string; note?: string }>) {
  return <div className="score-metric"><span>{label}</span><strong>{value}</strong>{note ? <small>{note}</small> : null}</div>;
}

function rank(value?: number, size?: number) {
  return value && size ? `${value} / ${size}` : "—";
}

export function statisticsReason(reason?: string) {
  return ({ not_public: "学校未公开", small_cohort: "参比人数不足，不展示", unpublished: "尚未发布", scope_unavailable: "参比范围尚未具备", unavailable: "统计尚未具备" } as Record<string, string>)[reason ?? ""] ?? "统计原因尚未核实";
}
function rankReason(reason?: string, value?: number) { return value ? "仅与同范围参试学生比较" : statisticsReason(reason); }

export function ExamDiagnosis({ questions, onStart }: Readonly<{ questions: StudentQuestion[]; onStart: () => void }>) {
  const missed = questions.filter((item) => item.score < item.max_score);
  if (missed.length === 0) return null;
  const partial = missed.filter((item) => item.score > 0);
  const biggest = [...missed].sort((a, b) => (b.max_score - b.score) - (a.max_score - a.score)).slice(0, 3);
  const knowledge = [...new Set(missed.flatMap((item) => item.knowledge_points ?? []))].slice(0, 3);
  return <section className="diagnosis" aria-labelledby="diagnosis-title">
    <div><h2 id="diagnosis-title">重点复盘</h2><p>{missed.length} 道失分题 · {partial.length} 道部分得分</p></div>
    <div className="diagnosis-list"><span>主要失分</span>{biggest.map((item) => <p key={item.question_id}>第 {item.question_no} 题 <strong>−{formatScore(item.max_score - item.score)} 分</strong></p>)}</div>
    {knowledge.length > 0 ? <div className="diagnosis-list"><span>相关知识</span>{knowledge.map((item) => <p key={item}>{item}</p>)}</div> : null}
    <button type="button" className="primary-action" onClick={onStart}>查看失分题 <span>→</span></button>
  </section>;
}

export function formatScore(value: number) {
  return Number.isInteger(value) ? String(value) : value.toFixed(1).replace(/\.0$/, "");
}
