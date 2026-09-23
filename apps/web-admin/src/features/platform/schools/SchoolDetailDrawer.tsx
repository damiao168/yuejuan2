import { Drawer, Tabs } from "antd";
import type { PlatformSchoolSummary } from "../../../api/platformSchools";
import { fullDate, relativeTime } from "./schoolPresentation";
import { SchoolOverviewTab } from "./SchoolOverviewTab";
import { SchoolMembersTab } from "./SchoolMembersTab";
import { SchoolUsageTab } from "./SchoolUsageTab";
import { SchoolModelsTab } from "./SchoolModelsTab";
import { SchoolActivityTab } from "./SchoolActivityTab";
import type { SchoolDetailTab } from "./SchoolSummaryTable";

interface Props {
  school: PlatformSchoolSummary | null;
  activeTab: SchoolDetailTab;
  overviewFocus: "created" | "status" | null;
  membersFocus: "administrators" | null;
  onTabChange: (tab: SchoolDetailTab) => void;
  onClose: () => void;
}

export function SchoolDetailDrawer({ school, activeTab, overviewFocus, membersFocus, onTabChange, onClose }: Props) {
  return <Drawer className="platform-school-drawer" open={Boolean(school)} onClose={onClose} width="min(940px, 96vw)" destroyOnHidden title={school ? <div className="platform-school-drawer-heading">
    <div><h2>{school.name}</h2><span className="mono">{school.code}</span></div>
    <span className={`platform-school-status ${school.status}`}>{school.status === "active" ? "使用中" : "已停用"}</span>
  </div> : "学校详情"}>
    {school ? <>
      <div className="platform-school-drawer-meta">创建于 {fullDate(school.created_at)} · 最近业务活动 {relativeTime(school.last_activity_at)}</div>
      <Tabs activeKey={activeTab} onChange={(key) => onTabChange(key as SchoolDetailTab)} destroyOnHidden items={[
        { key: "overview", label: "概览", children: <SchoolOverviewTab school={school} focus={overviewFocus} /> },
        { key: "members", label: "成员与账号", children: <SchoolMembersTab tenantId={school.tenant_id} studentCount={school.members.students} focus={membersFocus} /> },
        { key: "usage", label: "AI 用量", children: <SchoolUsageTab tenantId={school.tenant_id} /> },
        { key: "models", label: "模型服务", children: <SchoolModelsTab tenantId={school.tenant_id} /> },
        { key: "activity", label: "活动与安全", children: <SchoolActivityTab tenantId={school.tenant_id} /> }
      ]} />
    </> : null}
  </Drawer>;
}
