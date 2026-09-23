import { useEffect, useState } from "react";
import { Alert, Button, Spin } from "antd";
import { getPlatformSchoolModelHealth, type SchoolModelHealthResponse } from "../../../api/platformSchools";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { fullDate } from "./schoolPresentation";

const roleLabel = { primary_a: "主评 A", primary_b: "主评 B", arbiter: "仲裁" };

export function SchoolModelsTab({ tenantId }: { tenantId: string }) {
  const [data, setData] = useState<SchoolModelHealthResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  useEffect(() => {
    let active = true;
    setLoading(true); setError(false);
    void getPlatformSchoolModelHealth(tenantId).then((result) => { if (active) setData(result); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [tenantId]);
  if (loading) return <div className="platform-school-panel-loading"><Spin /></div>;
  if (error) return <Alert type="error" showIcon message="模型服务加载失败" />;
  if (!data || data.default_model.status === "unconfigured") return <div className="platform-school-empty-model"><h3>尚未配置 AI 模型</h3><p>为这所学校配置并检测默认模型后，将显示连接与能力状态。</p><Button href={`#/platform/model-config?tenant_id=${encodeURIComponent(tenantId)}`}>配置模型</Button></div>;
  const model = data.default_model;
  return <div className="platform-school-detail-sections">
    <section><h3>默认模型</h3><div className="platform-school-model-head"><strong>{model.model_name}</strong><span>{model.display_name || model.provider_key}</span></div><dl className="platform-school-facts">
      <div><dt>API 连接</dt><dd>{model.connection_status === "success" ? "正常" : model.connection_status === "failed" ? "异常" : "未检测"}</dd></div>
      <div><dt>能力检测</dt><dd>{model.capability_status === "success" ? "通过" : model.capability_status === "failed" ? "未通过" : "未检测"}</dd></div>
      <div><dt>检测延迟</dt><dd>{model.latency_ms ? `${model.latency_ms} ms` : "—"}</dd></div>
      <div><dt>最近检测</dt><dd>{fullDate(model.last_tested_at)}</dd></div>
      <div><dt>API Key</dt><dd>{model.credential_hint || "已加密保存"}</dd></div>
    </dl>{model.connection_message || model.capability_message ? <p className="platform-school-readonly-note">{model.connection_message || model.capability_message}</p> : null}</section>
    <section><h3>三智能体模型</h3>{data.roles.length ? <ResponsiveTable<SchoolModelHealthResponse["roles"][number]> className="dense-data-table platform-school-detail-table" rowKey="agent_role" pagination={false} dataSource={data.roles} columns={[
      { title: "智能体", dataIndex: "agent_role", width: 120, render: (value: keyof typeof roleLabel) => roleLabel[value] },
      { title: "模型", dataIndex: "model_name" },
      { title: "状态", dataIndex: "status", width: 100, render: (value: string) => value === "healthy" ? "正常" : value === "disabled" ? "已停用" : "需检测" }
    ]} /> : <p className="platform-school-readonly-note">尚未配置主评与仲裁模型。</p>}</section>
    <Button href={`#/platform/model-config?tenant_id=${encodeURIComponent(tenantId)}`}>前往模型配置</Button>
  </div>;
}
