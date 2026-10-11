import { Tag, Tooltip } from "antd";

export function MockBadge({ compact = false }: Readonly<{ compact?: boolean }>) {
  return (
    <Tooltip title="该页面展示的是演示用示例数据，不是真实业务数据">
      <Tag color="gold" className="mock-badge">
        {compact ? "示例" : "示例数据"}
      </Tag>
    </Tooltip>
  );
}
