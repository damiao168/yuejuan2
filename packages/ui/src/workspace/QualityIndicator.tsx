import { StatusBadge } from "./StatusBadge";

/** 优先展示阻断数量；没有阻断时才展示警告数量，两者为零时显示正常。 */
export function QualityIndicator({ blockers, warnings }: Readonly<{ blockers: number; warnings: number }>) {
  if (blockers > 0) return <StatusBadge tone="danger">{blockers} 项阻断</StatusBadge>;
  if (warnings > 0) return <StatusBadge tone="warning">{warnings} 项需关注</StatusBadge>;
  return <StatusBadge tone="success">质量正常</StatusBadge>;
}
