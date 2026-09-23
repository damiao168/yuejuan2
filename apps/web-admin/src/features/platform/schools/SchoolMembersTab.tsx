import { useEffect, useMemo, useState } from "react";
import { Alert, Input, Select, Spin, Tooltip, type TableColumnsType } from "antd";
import { Search } from "lucide-react";
import { listPlatformSchoolMembers, type SchoolMembersResponse } from "../../../api/platformSchools";
import type { ManagedUser } from "../../../api/users";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { fullDate, roleLabel, shortDate } from "./schoolPresentation";

export function SchoolMembersTab({ tenantId }: { tenantId: string }) {
  const [data, setData] = useState<SchoolMembersResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const [query, setQuery] = useState("");
  const [role, setRole] = useState("");
  const [status, setStatus] = useState("");

  useEffect(() => {
    let active = true;
    setLoading(true); setError(false);
    void listPlatformSchoolMembers(tenantId).then((result) => { if (active) setData(result); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [tenantId]);

  const visible = useMemo(() => (data?.members ?? []).filter((item) => {
    const matchesQuery = !query || `${item.display_name} ${item.username} ${item.employee_no ?? ""}`.toLowerCase().includes(query.toLowerCase());
    return matchesQuery && (!role || item.roles.includes(role)) && (!status || item.status === status);
  }), [data, query, role, status]);

  const columns: TableColumnsType<ManagedUser> = [
    { title: "姓名", dataIndex: "display_name", width: 120 },
    { title: "账号", dataIndex: "username", width: 145, render: (value: string) => <span className="mono">{value}</span> },
    { title: "角色", dataIndex: "roles", width: 145, render: (roles: string[]) => roles.map((item) => roleLabel[item] ?? item).join("、") || "—" },
    { title: "状态", dataIndex: "status", width: 70, render: (value: string) => value === "active" ? "正常" : "停用" },
    { title: "创建日期", dataIndex: "created_at", width: 105, render: (value?: string) => <Tooltip title={fullDate(value)}>{shortDate(value)}</Tooltip> },
    { title: "最后登录", dataIndex: "last_login_at", width: 125, render: (value?: string) => <Tooltip title={fullDate(value)}>{value ? shortDate(value) : "从未登录"}</Tooltip> },
    { title: "账号信息", key: "info", width: 150, render: (_, item) => <Tooltip title={`激活：${fullDate(item.activated_at)}`}><span>{item.phone_masked || item.employee_no || "—"}</span></Tooltip> }
  ];

  if (loading) return <div className="platform-school-panel-loading"><Spin /></div>;
  if (error) return <Alert type="error" message="账号信息加载失败" showIcon />;
  return <div className="platform-school-tab-stack">
    <div className="platform-school-inline-summary">共 {data?.summary.total ?? 0} 个账号 · {data?.summary.active ?? 0} 正常 · {data?.summary.disabled ?? 0} 停用 · {data?.summary.admins ?? 0} 管理员</div>
    <div className="platform-school-member-filters">
      <Input prefix={<Search size={15} />} placeholder="搜索姓名或账号" allowClear value={query} onChange={(event) => setQuery(event.target.value)} />
      <Select aria-label="账号角色" value={role || "all"} onChange={(value) => setRole(value === "all" ? "" : value)} options={[{label:"全部角色",value:"all"},{label:"学校管理员",value:"school_admin"},{label:"教师",value:"teacher"},{label:"阅卷员",value:"grader"},{label:"仲裁员",value:"arbitrator"}]} />
      <Select aria-label="账号状态" value={status || "all"} onChange={(value) => setStatus(value === "all" ? "" : value)} options={[{label:"全部状态",value:"all"},{label:"正常",value:"active"},{label:"停用",value:"disabled"}]} />
    </div>
    <ResponsiveTable<ManagedUser> className="dense-data-table" rowKey="id" columns={columns} dataSource={visible} pagination={{ pageSize: 15, showSizeChanger: false }} scroll={{ x: 860 }} locale={{ emptyText: "没有符合条件的账号" }} />
    <p className="platform-school-readonly-note">平台视图仅供排查。账号启停、角色调整和凭据恢复仍由学校内的受控流程处理。</p>
  </div>;
}
