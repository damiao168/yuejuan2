import { Button, Empty, List } from "antd";
import { ShieldCheck } from "lucide-react";
import { getSafeUserText } from "../../../api/client";
import type { QualityCheckResult, QualityIssue } from "../../../api/scores";
import { EmptyState } from "../../../components/PageState";
import { StatusTag } from "../../../components/StatusTag";

const qualityLabels: Record<string, string> = {
  unfinished_review_tasks: "还有阅卷任务未完成",
  unfinished_arbitration_tasks: "还有仲裁任务未完成",
  ocr_failed_unhandled: "识别失败（未处理）",
  missing_final_grades: "部分题目还没有最终得分",
  grades_not_confirmed: "成绩未确认",
  no_submission_grades: "无成绩可发布",
  missing_submission_unresolved: "应考学生尚未匹配答卷",
  unidentified_submission: "答卷身份未确认或存在重复",
  missing_pages_unresolved: "答卷缺页或页面质量异常"
};

export function QualityPanel({ quality, publishedOrLocked, actionFor }: Readonly<{ quality: QualityCheckResult | null; publishedOrLocked: boolean; actionFor?: (code: string) => { label: string; run: () => void } | undefined }>) {
  if (!quality) {
    return <EmptyState title="暂无质量检查" description="选择考试后，这里会列出发布前需要处理的问题。" />;
  }
  if (quality.quality.passed) {
    return <div className="score-quality-pass">
      <ShieldCheck size={22} />
      <div>
        <strong>基础成绩检查通过</strong>
        <span>{publishedOrLocked ? "基础成绩已锁定；本项检查通过。门户发布状态见版本区域。" : "本项检查通过；请继续核对门户发布检查。"}</span>
      </div>
    </div>;
  }
  return <List
    size="small"
    dataSource={quality.quality.issues}
    locale={{ emptyText: <Empty description="暂无质量问题" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
    renderItem={(issue: QualityIssue) => <List.Item>
      <div className="score-quality-issue" title={getSafeUserText(issue.message, "成绩质量检查未通过")}>
        <StatusTag tone={issue.blocking ? "danger" : "warning"}>{issue.blocking ? "须处理后才能发布" : "提醒"}</StatusTag>
        <strong>{qualityLabels[issue.code] ?? "其他发布检查问题"}</strong>
        <span>{issue.count} 项</span>
        {actionFor?.(issue.code) ? <Button size="small" onClick={actionFor(issue.code)!.run}>{actionFor(issue.code)!.label}</Button> : null}
      </div>
    </List.Item>}
  />;
}
