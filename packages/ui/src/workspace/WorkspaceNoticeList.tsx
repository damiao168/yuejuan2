import type { WorkspaceNotice } from "./types";

export function WorkspaceNoticeList({ notices, emptyText, onNavigate }: Readonly<{ notices: WorkspaceNotice[]; emptyText: string; onNavigate: (route: string) => void }>) {
  if (notices.length === 0) {
    return <div className="eg-workspace-notice-empty">{emptyText}</div>;
  }
  return (
    <div className="eg-workspace-notices">
      {notices.map((notice) => (
        <div className={`eg-workspace-notice is-${notice.severity}`} key={notice.code}>
          <div><strong>{notice.title}</strong><p>{notice.message}</p></div>
          {notice.action_route ? <button type="button" onClick={() => onNavigate(notice.action_route!)}>{notice.action_label ?? "查看"}</button> : null}
        </div>
      ))}
    </div>
  );
}
