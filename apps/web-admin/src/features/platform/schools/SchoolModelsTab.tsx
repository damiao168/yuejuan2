import { useEffect, useState } from "react";
import { Alert, Spin } from "antd";
import { getPlatformSchoolModelHealth, type SchoolModelHealthResponse } from "../../../api/platformSchools";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { InternalRouteButton } from "../../../components/InternalRouteButton";
import { fullDate } from "./schoolPresentation";

const roleLabel = { primary_a: "主评 A", primary_b: "主评 B", arbiter: "仲裁" };

function detectionLabel(status: string | undefined, success: string, failed: string): string {
  if (status === "success") return success;
  if (status === "failed") return failed;
  if (status === "temporary_unavailable") return "暂时无法验证";
  return "未检测";
}

function agentStatusLabel(status: string): string {
  if (status === "healthy") return "正常";
  if (status === "disabled") return "已停用";
  return "需检测";
}

export function SchoolModelsTab({ tenantId }: Readonly<{ tenantId: string }>) {
  const [data, setData] = useState<SchoolModelHealthResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  useEffect(() => {
    // 每轮请求独立持有有效标记，学校切换后旧健康检查结果不能覆盖当前学校。
    let active = true;
    setLoading(true); setError(false);
    void getPlatformSchoolModelHealth(tenantId).then((result) => { if (active) setData(result); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [tenantId]);
  if (loading) return <div className="platform-school-panel-loading"><Spin /></div>;
  if (error) return <Alert type="error" showIcon message="模型服务加载失败" />;
  if (!data) return <Alert type="warning" showIcon message="暂时无法确认模型状态" />;
  const model = data.default_model;
  const structuredLabel = model.capability_status === "success" && model.capability_version !== "structured-json-v4"
    ? "待按新策略复验" : detectionLabel(model.capability_status, "通过", "未通过");
  const configURL = `/admin/platform/model-config?tenant_id=${encodeURIComponent(tenantId)}`;
  // 对话默认配置与评分角色独立；缺少默认模型时仍须展示已经存在的评分绑定。
  return <div className="platform-school-detail-sections">
    <section><h3>日常对话默认模型</h3>{model.status === "unconfigured" ? <div className="platform-school-empty-model"><p>尚未指定学校默认对话模型。下方评分模型独立配置；日常对话的回退方式由平台设置决定。</p><InternalRouteButton href={`${configURL}&tab=models`}>配置对话模型</InternalRouteButton></div> : <><div className="platform-school-model-head"><strong>{model.model_name}</strong><span>{model.display_name || model.provider_key}</span></div><dl className="platform-school-facts">
      <div><dt>API 连接</dt><dd>{detectionLabel(model.connection_status, "正常", "异常")}</dd></div>
      <div><dt>普通回复</dt><dd>{detectionLabel(model.text_status, "通过", "未通过")}</dd></div>
      <div><dt>结构化输出（评分/解析）</dt><dd>{structuredLabel}</dd></div>
      <div><dt>检测延迟</dt><dd>{model.latency_ms ? `${model.latency_ms} ms` : "—"}</dd></div>
      <div><dt>最近检测</dt><dd>{fullDate(model.last_tested_at)}</dd></div>
      <div><dt>最近回复检测</dt><dd>{fullDate(model.last_text_tested_at)}</dd></div>
      <div><dt>API Key</dt><dd>{model.credential_hint || "已加密保存"}</dd></div>
    </dl><p className="platform-school-readonly-note">普通回复通过即可用于日常对话；结构化输出用于评分和试卷解析，角色状态在下方单独展示。</p>{model.connection_message || model.text_message ? <p className="platform-school-readonly-note">{model.connection_message || model.text_message}</p> : null}{model.capability_message ? <p className="platform-school-readonly-note">结构化输出：{model.capability_message}</p> : null}</>}</section>
    <section><h3>三智能体评分模型</h3><p className="platform-school-readonly-note">角色状态用于排查配置，不代表正式评分准入已经通过。</p>{data.roles.length ? <ResponsiveTable<SchoolModelHealthResponse["roles"][number]> className="dense-data-table platform-school-detail-table" rowKey="agent_role" pagination={false} dataSource={data.roles} columns={[
      { title: "智能体", dataIndex: "agent_role", width: 120, render: (value: keyof typeof roleLabel) => roleLabel[value] },
      { title: "模型", dataIndex: "model_name" },
      { title: "状态", dataIndex: "status", width: 100, render: agentStatusLabel }
    ]} /> : <p className="platform-school-readonly-note">尚未配置主评与仲裁模型。</p>}</section>
    <InternalRouteButton href={`${configURL}&tab=bindings`}>配置评分模型</InternalRouteButton>
  </div>;
}
