import { useMemo, useState } from "react";
import { Button, Drawer, Empty, type TableColumnsType } from "antd";
import { Download } from "lucide-react";
import type { RosterEntry, RosterReport } from "../../../api/scores";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { rosterCounts, type ReleaseStatusModel } from "../releaseStatus";

// 名单抽屉先列需要处理的记录，再列缺考，最后是已有成绩的学生。
function rosterOrder(entry: RosterEntry) {
  if (entry.status === "absent") return 1;
  return ["graded", "awaiting_aggregation"].includes(entry.resolution_code) ? 2 : 0;
}

/**
 * 成绩发布页的状态行：学生现在能否看到成绩、唯一的主按钮，以及发给谁的人数摘要。
 * “还有 N 项待处理”只做页面定位，所以只读账号也能使用；发布、更正和导出需要写权限。
 */
export function ScoreReleaseStatus({ model, canWrite, actioning, onPrimary, onExport, exportBlocked, roster, rosterColumns, sessionText, activeRegrades, onOpenRegrades, historyCount, onOpenHistory }: Readonly<{
  model: ReleaseStatusModel;
  canWrite: boolean;
  actioning: string | null;
  onPrimary: () => void;
  onExport: () => void;
  exportBlocked: boolean;
  roster: RosterReport | null;
  rosterColumns: TableColumnsType<RosterEntry>;
  sessionText: string;
  activeRegrades: number;
  onOpenRegrades?: () => void;
  historyCount: number;
  onOpenHistory: () => void;
}>) {
  const [rosterOpen, setRosterOpen] = useState(false);
  const counts = rosterCounts(roster);
  const entries = useMemo(() => [...(roster?.entries ?? [])].sort((a, b) => rosterOrder(a) - rosterOrder(b)), [roster]);
  const primary = model.primary;
  const exporting = actioning === "export-official";
  const primaryDisabled = !primary || (primary.kind !== "locate" && (!canWrite || Boolean(actioning))) || (primary.kind === "export" && exportBlocked);
  const primaryLoading = primary?.kind === "export" ? exporting : actioning === "release-publish";

  return <section className="score-release-status" aria-label="学生端发布状态">
    <div className="score-release-status-line">
      <div>
        <strong className={`score-release-status-text tone-${model.tone}`}>{model.text}</strong>
        {sessionText ? <span className="score-release-status-note">{sessionText}</span> : null}
      </div>
      <div className="score-release-status-actions">
        {historyCount ? <Button onClick={onOpenHistory}>发布记录（{historyCount}）</Button> : null}
        {primary?.kind === "correction" ? <Button icon={<Download size={16} />} disabled={!canWrite || Boolean(actioning) || exportBlocked} loading={exporting} onClick={onExport}>导出教师成绩表</Button> : null}
        {primary ? <Button type="primary" icon={primary.kind === "export" ? <Download size={16} /> : undefined} disabled={primaryDisabled} loading={primaryLoading} onClick={onPrimary}>{primary.label}</Button> : null}
      </div>
    </div>
    {counts ? <div className="score-release-status-roster">
      <span>应考 {counts.expected} · 收到答卷 {counts.received} · 按缺考计 {counts.absent} 人</span>
      <Button type="link" size="small" onClick={() => setRosterOpen(true)}>查看名单</Button>
      {counts.absent ? <span className="score-release-status-note">没有匹配答卷的学生会自动按缺考计；发布前请确认没有漏扫或未识别的答卷。</span> : null}
    </div> : null}
    {activeRegrades && onOpenRegrades ? <div className="score-release-status-roster">
      <span>{activeRegrades} 项题目级复评进行中</span>
      <Button type="link" size="small" onClick={onOpenRegrades}>查看复评</Button>
    </div> : null}
    <Drawer title="考试名单" width="min(1080px, 96vw)" open={rosterOpen} onClose={() => setRosterOpen(false)}>
      <ResponsiveTable
        className="dense-data-table score-roster-table"
        rowKey="key"
        size="small"
        columns={rosterColumns}
        dataSource={entries}
        pagination={{ pageSize: 20, showSizeChanger: false }}
        locale={{ emptyText: <Empty description="本场考试尚未关联有效班级名册。" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }}
      />
    </Drawer>
  </section>;
}
