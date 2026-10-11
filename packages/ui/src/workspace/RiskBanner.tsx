import type { WorkspaceNotice } from "./types";
import { WorkspaceNoticeList } from "./WorkspaceNoticeList";

/** 先展示阻塞事实，再展示警告，并保留对应操作入口。 */
export function RiskBanner({
  blockers,
  warnings,
  onNavigate,
  title = "阻断与风险"
}: Readonly<{
  blockers: WorkspaceNotice[];
  warnings: WorkspaceNotice[];
  onNavigate: (route: string) => void;
  title?: string;
}>) {
  return (
    <section className="eg-risk-banner">
      <div className="eg-risk-banner-heading"><h2>{title}</h2><small>{blockers.length} 项阻断</small></div>
      <WorkspaceNoticeList notices={blockers} emptyText="当前没有阻断项" onNavigate={onNavigate} />
      {warnings.length > 0 ? (
        <div className="eg-risk-banner-warnings">
          <span>需要关注</span>
          <WorkspaceNoticeList notices={warnings} emptyText="" onNavigate={onNavigate} />
        </div>
      ) : null}
    </section>
  );
}
