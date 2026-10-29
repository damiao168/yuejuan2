import { Alert, Button, Table } from "antd";
import { useReviewStatistics, type ReviewStatisticsScope } from "../hooks/useReviewStatistics";
import type { ReviewQuestionStatistics } from "../../../../api/reviewStatistics";
import "./review-statistics.css";

export function reviewAverageLabel(value: number | null | undefined) {
  return typeof value === "number" && Number.isFinite(value) ? `${Number(value.toFixed(2))} 分` : "—";
}

export function ReviewStatisticsPanel(props: Readonly<ReviewStatisticsScope & { questionId?: string; questionNo?: string }>) {
  const { statistics, error, loading, focused, refresh } = useReviewStatistics(props);
  const title = props.personalScope ? "本人逐题平均分" : "本场逐题平均分";
  const current = statistics?.questions.find(question => question.question_id === props.questionId);
  const table = <Table<ReviewQuestionStatistics> rowKey="question_id" dataSource={statistics?.questions ?? []} size="small" pagination={{ pageSize: 10 }} locale={{ emptyText: "暂无已正式评分题目" }} columns={[
    { title: "题号", dataIndex: "question_no", render: value => `第 ${value} 题` },
    { title: "平均分", dataIndex: "average_score", render: reviewAverageLabel },
    { title: "已评分份数", dataIndex: "graded_count" }
  ]} />;
  return <section className="review-statistics-panel" aria-label={title}>
    <div className="review-statistics-summary"><h2>{title}</h2>
      {props.examId && props.questionId ? <div className="review-statistics-totals" aria-live="polite">
        <div><span>第 {props.questionNo || current?.question_no || "—"} 题平均分</span><strong>{reviewAverageLabel(current?.average_score)}</strong></div>
        <div><span>已评分</span><strong>{current?.graded_count ?? (statistics ? 0 : "—")}<small> 份</small></strong></div>
      </div> : !props.examId ? <p>选择考试后查看逐题平均分。</p> : null}
      <Button size="small" loading={loading} disabled={!props.examId || !focused} title={!focused ? "窗口未激活，自动更新已暂停" : "每 10 秒更新，交分后立即更新"} onClick={refresh}>更新统计</Button>
    </div>
    {props.examId ? <>
      {error ? <Alert type="warning" showIcon message={error} description={statistics ? "下方保留上次成功读取的统计。" : undefined} /> : null}
      {props.questionId ? <details><summary>查看全部题目平均分</summary>{table}</details> : table}
      <details><summary>统计口径</summary>
        <p className="muted">{!focused ? "窗口未激活，自动更新已暂停。" : loading ? "正在更新统计…" : statistics ? `最近更新 ${new Date(statistics.updated_at).toLocaleTimeString("zh-CN", { hour12: false })}` : "统计尚未读取。"}</p>
        <p>每题平均分 = 该题已正式评分的分数合计 / 该题已评分份数，零分计入，未评答卷不计入。{props.personalScope ? "仅统计本人正式提交的评阅意见。" : "仅统计本场已确认的当前最终成绩，同一答题片段仅统计一次。"}</p>
      </details>
    </> : null}
  </section>;
}
