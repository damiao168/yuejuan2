import { Empty, type TableColumnsType } from "antd";
import { Unplug } from "lucide-react";
import type { ManagedModelAPIConfig } from "../../../api/modelApiConfig";
import { ResponsiveTable } from "../../../components/ResponsiveTable";

export function SchoolModelTable({ tenantID, loading, columns, configs }: {
  tenantID: string;
  loading: boolean;
  columns: TableColumnsType<ManagedModelAPIConfig>;
  configs: ManagedModelAPIConfig[];
}) {
  return <section className="platform-model-table-shell">
    {tenantID ? <ResponsiveTable<ManagedModelAPIConfig>
      className="dense-data-table"
      rowKey="id"
      loading={loading}
      columns={columns}
      dataSource={configs}
      pagination={false}
      locale={{ emptyText: <Empty image={<Unplug size={38} />} description={<span>暂未配置 AI 模型<br /><small>选择供应商并准备 API Key 即可获取模型</small></span>} /> }}
    /> : <Empty description="请先选择学校" />}
  </section>;
}
