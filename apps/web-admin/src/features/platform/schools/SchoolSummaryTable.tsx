import { App, Button, Dropdown, Tooltip, type TableColumnsType } from "antd";
import { AlertTriangle, MoreHorizontal } from "lucide-react";
import type { PlatformSchoolSummary, UsageWindow } from "../../../api/platformSchools";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { fullDate, numberText, relativeTime, shortDate, tokenText } from "./schoolPresentation";

export type SchoolDetailTab = "overview" | "members" | "usage" | "models" | "activity";

interface Props {
  schools: PlatformSchoolSummary[];
  loading: boolean;
  usageWindow: UsageWindow;
  onOpen: (school: PlatformSchoolSummary, tab: SchoolDetailTab) => void;
  onChangeStatus: (school: PlatformSchoolSummary) => void;
}

const healthLabel = { healthy: "正常", warning: "异常", unconfigured: "未配置" };

export function SchoolSummaryTable({ schools, loading, usageWindow, onOpen, onChangeStatus }: Props) {
  const { modal } = App.useApp();
  const confirmStatus = (item: PlatformSchoolSummary) => modal.confirm({
    title: item.status === "active" ? "停用这所学校？" : "重新启用这所学校？",
    content: item.status === "active" ? "停用后该学校账号将无法登录。" : `确认重新启用“${item.name}”？`,
    okText: "确认", cancelText: "取消", okButtonProps: { danger: item.status === "active" },
    onOk: () => onChangeStatus(item)
  });
  const columns: TableColumnsType<PlatformSchoolSummary> = [
    { title: "学校", key: "school", width: 190, render: (_, item) => <div className="platform-school-identity">
      {item.attention_reasons?.length ? <Tooltip title={<><strong>{item.attention_reasons.length} 项需要关注</strong><br />{item.attention_reasons.map((reason) => <span key={reason}>{reason}<br /></span>)}</>}><AlertTriangle size={14} className="platform-school-warning" /></Tooltip> : null}
      <span><button type="button" onClick={(event) => { event.stopPropagation(); onOpen(item, "overview"); }}>{item.name}</button><small>{item.code}</small></span>
    </div> },
    { title: "管理员", key: "administrator", width: 170, render: (_, item) => <Tooltip title={`管理员 ${item.administrator.admin_count} 人${item.administrator.last_login_at ? ` · 最近登录 ${fullDate(item.administrator.last_login_at)}` : ""}`}>
      <div className="platform-school-cell-pair"><strong>{item.administrator.display_name || "未配置"}</strong><small>{item.administrator.username || "—"}{item.administrator.admin_count > 1 ? ` · +${item.administrator.admin_count - 1}` : ""}</small></div>
    </Tooltip> },
    { title: "账号 / 学生", key: "members", width: 125, render: (_, item) => <Tooltip title={<div>管理员 {numberText(item.members.administrators)}<br />教师 {numberText(item.members.teachers)}<br />阅卷员 {numberText(item.members.graders)}<br />学生 {numberText(item.members.students)}<br />班级 {numberText(item.members.classes)}</div>}>
      <div className="platform-school-cell-pair"><strong>{numberText(item.members.accounts)} / {numberText(item.members.students)}</strong><small>账号 / 学生</small></div>
    </Tooltip> },
    { title: "AI 用量", key: "usage", width: 135, render: (_, item) => <Tooltip title={<div>{usageWindow === "today" ? "今天" : `近 ${item.usage.window_days} 天`}<br />输入 Token {numberText(item.usage.input_tokens)}<br />输出 Token {numberText(item.usage.output_tokens)}<br />总 Token {numberText(item.usage.total_tokens)}<br />AI 请求 {numberText(item.usage.requests)}</div>}>
      <div className="platform-school-cell-pair"><strong>{tokenText(item.usage.total_tokens)}</strong><small>{numberText(item.usage.requests)} 次</small></div>
    </Tooltip> },
    { title: "模型", key: "model", width: 100, render: (_, item) => <Tooltip title={<div>默认模型 {item.model_health.model_name || "未配置"}<br />连接 {item.model_health.connection_status || "—"}<br />能力检测 {item.model_health.capability_status || "—"}<br />最后检测 {item.model_health.last_tested_at ? fullDate(item.model_health.last_tested_at) : "—"}</div>}>
      <button className={`platform-school-health ${item.model_health.status}`} type="button" onClick={(event) => { event.stopPropagation(); onOpen(item, "models"); }}><i />{healthLabel[item.model_health.status]}</button>
    </Tooltip> },
    { title: "最近活跃", key: "activity", width: 110, render: (_, item) => <Tooltip title={fullDate(item.last_activity_at)}><span>{relativeTime(item.last_activity_at)}</span></Tooltip> },
    { title: "创建时间", key: "created", width: 105, render: (_, item) => <Tooltip title={fullDate(item.created_at)}><span>{shortDate(item.created_at)}</span></Tooltip> },
    { title: "状态", key: "status", width: 85, render: (_, item) => <span className={`platform-school-status ${item.status}`}>{item.status === "active" ? "使用中" : "已停用"}</span> },
    { title: "操作", key: "actions", width: 48, render: (_, item) => <Dropdown trigger={["click"]} menu={{ items: [
      { key: "overview", label: "查看学校", onClick: () => onOpen(item, "overview") },
      { key: "members", label: "成员与账号", onClick: () => onOpen(item, "members") },
      { key: "usage", label: "AI 用量", onClick: () => onOpen(item, "usage") },
      { key: "models", label: "模型服务", onClick: () => onOpen(item, "models") },
      { key: "activity", label: "活动记录", onClick: () => onOpen(item, "activity") },
      { type: "divider" },
      { key: "status", danger: item.status === "active", label: item.status === "active" ? "停用学校" : "重新启用", onClick: () => confirmStatus(item) }
    ] }}><Button type="text" size="small" aria-label={`${item.name} 的更多操作`} icon={<MoreHorizontal size={18} />} onClick={(event) => event.stopPropagation()} /></Dropdown> }
  ];

  return <ResponsiveTable<PlatformSchoolSummary> className="dense-data-table platform-school-table" rowKey="tenant_id" loading={loading} columns={columns} dataSource={schools} pagination={false} scroll={{ x: 1068 }} onRow={(item) => ({ onClick: (event) => { if (!(event.target as HTMLElement).closest("button, a, .ant-dropdown")) onOpen(item, "overview"); } })} locale={{ emptyText: "当前条件下暂无学校" }} />;
}
