import type { ReactNode } from "react";
import { Empty, type TableColumnsType } from "antd";
import { Unplug } from "lucide-react";
import type { ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { ResponsiveTable } from "../../../components/ResponsiveTable";

export function SchoolModelTable({ tenantID, loading, columns, configs, usageLabels = {} }: Readonly<{
  tenantID: string;
  loading: boolean;
  columns: TableColumnsType<ManagedModelAPIConfig>;
  configs: ManagedModelAPIConfig[];
  usageLabels?: Record<string, string[]>;
}>) {
  const identity = columns.filter(column => column.key === "model-name" || column.key === "provider");
  const settings = columns.filter(column => column.key !== "model-name" && column.key !== "provider");
  const summaryColumns: TableColumnsType<ManagedModelAPIConfig> = [
    ...identity,
    { title: "可用性", key: "availability", width: 140, render: (_, config) => config.status !== "active" ? "已停用" : !config.credential_configured ? "未配置密钥" : config.last_test_status === "success" ? "连接正常" : config.last_test_status === "failed" ? "连接失败" : config.last_test_status === "temporary_unavailable" ? "连接暂未确认" : "待检测" },
    { title: "已配置用途", key: "usage-summary", render: (_, config) => usageLabels[config.id]?.join("、") || "尚未配置用途" },
    { title: "设置", key: "settings", width: 160, render: (_, config, index) => <details className="platform-model-settings"><summary>设置与诊断</summary><div className="platform-model-setting-sections">{settings.map(column => <section key={String(column.key)} data-column-key={String(column.key)}><h4>{typeof column.title === "function" ? "模型设置" : column.title}</h4>{"render" in column && column.render ? column.render(undefined, config, index) as ReactNode : null}</section>)}</div></details> }
  ];
  return <section className="platform-model-table-shell">
    {tenantID ? <ResponsiveTable<ManagedModelAPIConfig>
      className="dense-data-table platform-model-table"
      rowKey="id"
      loading={loading}
      columns={summaryColumns}
      mobilePrimaryKeys={["model-name", "provider", "availability", "usage-summary", "settings"]}
      dataSource={configs}
      pagination={false}
      scroll={{ x: 850 }}
      locale={{ emptyText: <Empty image={<Unplug size={38} />} description={<span>暂未配置 AI 模型<br /><small>选择供应商并准备 API Key 即可获取模型</small></span>} /> }}
    /> : <Empty description="请先选择学校" />}
  </section>;
}
