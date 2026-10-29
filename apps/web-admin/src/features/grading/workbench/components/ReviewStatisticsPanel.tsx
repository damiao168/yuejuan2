import { Alert, Button, Table } from "antd";
import { useReviewStatistics, type ReviewStatisticsScope } from "../hooks/useReviewStatistics";
import type { ReviewQuestionStatistics } from "../../../../api/reviewStatistics";
import "./review-statistics.css";

export function reviewRateLabel(value: number | null | undefined) {
  return typeof value === "number" && Number.isFinite(value) ? `${(value * 100).toFixed(1)}%` : "—";
}

export function ReviewStatisticsPanel(props: Readonly<ReviewStatisticsScope>) {
  const { statistics, error, loading, focused, refresh } = useReviewStatistics(props);
  const title = props.personalScope ? "本人已评正确率" : "本场实时正确率";
  return <section className="review-statistics-panel" aria-label={title}>
    <div className="review-statistics-summary"><h2>{title}</h2>
      {props.examId ? <div className="review-statistics-totals" aria-live="polite">
        <div><span>正确率（满分）</span><strong>{reviewRateLabel(statistics?.correct_rate)}</strong></div>
        <div><span>已正式评分</span><strong>{statistics?.graded_count ?? "—"}<small> 题次</small></strong></div>
        <div><span>平均得分率</span><strong>{reviewRateLabel(statistics?.average_score_rate)}</strong></div>
      </div> : <p>选择考试后查看统计。</p>}
      <Button size="small" loading={loading} disabled={!props.examId || !focused} title={!focused ? "窗口未激活，自动更新已暂停" : "每 10 秒更新，交分后立即更新"} onClick={refresh}>更新统计</Button>
    </div>
    {props.examId ? <>
      {error ? <Alert type="warning" showIcon message={error} description={statistics ? "下方保留上次成功读取的统计。" : undefined} /> : null}
      <details><summary>逐题统计与口径</summary>
        <p className="muted">{!focused ? "窗口未激活，自动更新已暂停。" : loading ? "正在更新统计…" : statistics ? `最近更新 ${new Date(statistics.updated_at).toLocaleTimeString("zh-CN", { hour12: false })}` : "统计尚未读取。"}</p>
        <p>{props.personalScope ? "只含本人正式提交的评阅意见，不展示其他教师或最终合分。" : "只含本场已确认的当前最终成绩，同一答题片段仅统计一次。"}正确率为满分题次 / 已评分题次；未评题次不计入，平均得分率为正式分数合计 / 满分合计。</p>
        <Table<ReviewQuestionStatistics> rowKey="question_id" dataSource={statistics?.questions ?? []} size="small" pagination={{ pageSize: 10 }} locale={{ emptyText: "暂无已正式评分题目" }} columns={[
          { title: "题号", dataIndex: "question_no", render: value => `第 ${value} 题` },
          { title: "已评分", dataIndex: "graded_count" },
          { title: "满分", dataIndex: "full_score_count" },
          { title: "正确率", dataIndex: "correct_rate", render: reviewRateLabel },
          { title: "得分率", dataIndex: "average_score_rate", render: reviewRateLabel },
        ]} />
      </details>
    </> : null}
  </section>;
}
