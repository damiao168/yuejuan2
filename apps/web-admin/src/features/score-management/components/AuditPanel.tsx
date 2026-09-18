import { List } from "antd";
import type { AuditLog } from "../../../api/audit";
import { EmptyState } from "../../../components/PageState";

const auditActionLabels: Record<string, string> = {
  "score.finalized": "成绩汇总",
  "score.confirmed": "成绩确认",
  "score.published": "成绩发布",
  "score.exported": "成绩导出",
  "score.roster_attendance_updated": "名册出勤状态调整"
};

function formatTime(value?: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

export function AuditPanel({ canRead, logs }: { canRead: boolean; logs: AuditLog[] }) {
  if (!canRead) {
    return <EmptyState title="无权查看操作记录" description="您的账号没有查看操作记录的权限。导出、确认、发布等操作仍会被系统记录。" />;
  }
  if (logs.length === 0) {
    return <EmptyState title="暂无操作记录" description="该考试还没有成绩相关的操作记录。" />;
  }
  return <List
    size="small"
    dataSource={logs.slice(0, 8)}
    renderItem={(item) => <List.Item>
      <div className="score-audit-row">
        <strong title={item.action}>{auditActionLabels[item.action] ?? "成绩操作"}</strong>
        <span>{item.reason || "未填写原因"}</span>
        <small>{formatTime(item.created_at)}</small>
      </div>
    </List.Item>}
  />;
}
