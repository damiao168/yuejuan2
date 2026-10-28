import { Alert, Button, List } from "antd";
import { getSafeUserText } from "../../../api/client";
import { StatusTag } from "../../../components/StatusTag";
import { publicationChecklist } from "../publicationChecklist";
import type { ScoreReleaseGate } from "../../../api/scoreReleases";
import type { QualityCheckResult, RosterReport } from "../../../api/scores";

const labels: Record<string, string> = {
  unfinished_review_tasks: "完成待阅答卷", unfinished_arbitration_tasks: "完成待仲裁答卷",
  ocr_failed_unhandled: "处理识别失败", missing_final_grades: "补齐题目最终得分",
  grades_not_confirmed: "确认成绩", no_submission_grades: "汇总最终成绩",
  missing_submission_unresolved: "核查未收到的答卷", unidentified_submission: "确认答卷身份与重复记录",
  missing_pages_unresolved: "处理缺页与页面异常", roster_unresolved: "解决名册未匹配记录",
  question_coverage_incomplete: "核对题目覆盖与满分", score_integrity_mismatch: "核对总分与小题得分",
  release_snapshot_incomplete: "补齐成绩版本的小题记录", quality_questions_missing: "配置考试题目",
  regrade_release_pending: "完成复评并生成成绩版本"
};

export function ScorePublicationChecklist({ quality, gate, roster, actionFor }: {
  quality: QualityCheckResult | null; gate: ScoreReleaseGate | null; roster: RosterReport | null;
  actionFor: (code: string, route?: string) => { label: string; run: () => void } | undefined;
}) {
  const checklist = publicationChecklist(quality, gate, roster);
  return <section className="score-publication-checklist" aria-label="发布待办">
    <div className="panel-head"><h3>发布待办</h3><StatusTag tone={checklist.passed ? "success" : checklist.ready ? "warning" : "neutral"}>{checklist.passed ? "全部检查通过" : checklist.ready ? `${checklist.items.filter(item => item.blocking).length} 类待处理` : "检查待读取"}</StatusTag></div>
    {!checklist.ready ? <Alert type="info" showIcon message="检查尚未读取完整，暂不能确认发布条件。" /> : null}
    {checklist.items.length ? <List size="small" dataSource={checklist.items} renderItem={item => {
      const action = actionFor(item.code, item.actionRoute);
      return <List.Item><div className="score-release-gate-row">
        <StatusTag tone={item.blocking ? "danger" : "warning"}>{item.blocking ? "待处理" : "提醒"}</StatusTag>
        <div><strong>{labels[item.code] ?? "其他发布检查问题"}</strong><span>{item.count} 项</span><details><summary>核对来源</summary>{item.sources.map((source, index) => <p key={`${source.name}-${index}`}>{source.name}：{source.count} 项 · {getSafeUserText(source.message, "发布检查问题")}</p>)}<code>{item.code}</code></details></div>
        {action ? <Button size="small" onClick={action.run}>{action.label}</Button> : null}
      </div></List.Item>;
    }} /> : checklist.passed ? <p className="muted">可以创建版本并核对学生预览；正式发布时会重新检查。</p> : checklist.ready ? <Alert type="warning" showIcon message="当前检查尚未通过，请刷新核对最新记录。" /> : null}
  </section>;
}
