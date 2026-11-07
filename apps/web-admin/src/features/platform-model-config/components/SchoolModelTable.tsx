import { Empty, type TableColumnsType } from "antd";
import { Unplug } from "lucide-react";
import type { ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { ResponsiveTable } from "../../../components/ResponsiveTable";

export function SchoolModelTable({ tenantID, loading, columns, configs }: Readonly<{
  tenantID: string;
  loading: boolean;
  columns: TableColumnsType<ManagedModelAPIConfig>;
  configs: ManagedModelAPIConfig[];
}>) {
  // 检测与设置各自占列，沿用原渲染器的队列和权限约束；移动端也全部直显，不能再次藏入详情。
  const tableWidth = columns.reduce((width, column) => width + (typeof column.width === "number" ? column.width : 160), 0);
  return <section className="platform-model-table-shell">
    {tenantID ? <ResponsiveTable<ManagedModelAPIConfig>
      className="dense-data-table platform-model-table"
      rowKey="id"
      loading={loading}
      columns={columns}
      mobilePrimaryKeys={columns.map(column => String(column.key))}
      dataSource={configs}
      pagination={false}
      scroll={{ x: tableWidth }}
      locale={{ emptyText: <Empty image={<Unplug size={38} />} description={<span>暂未配置 AI 模型<br /><small>选择供应商并准备 API Key 即可获取模型</small></span>} /> }}
    /> : <Empty description="请先选择学校" />}
  </section>;
}
