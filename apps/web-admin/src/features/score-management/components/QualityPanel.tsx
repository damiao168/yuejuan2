import { Empty, List } from "antd";
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

export function QualityPanel({ quality, publishedOrLocked }: { quality: QualityCheckResult | null; publishedOrLocked: boolean }) {
  if (!quality) {
    return <EmptyState title="暂无质量检查" description="选择考试后，这里会列出发布前需要处理的问题。" />;
  }
  if (quality.quality.passed) {
    return <div className="score-quality-pass">
      <ShieldCheck size={22} />
      <div>
        <strong>{publishedOrLocked ? "成绩已发布，质量校验通过" : "发布前质量检查通过"}</strong>
        <span>{publishedOrLocked ? "成绩已发布并锁定，所有检查项均已处理。" : "所有检查项均已通过，可以发布成绩。"}</span>
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
        <strong>{qualityLabels[issue.code] ?? issue.code}</strong>
        <span>{issue.count} 项</span>
      </div>
    </List.Item>}
  />;
}
