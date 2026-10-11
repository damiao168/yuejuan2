import { StatusBadge } from "./StatusBadge";

/** 便于区分阻塞项和警告，同时保留各自数量。 */
export function QualityIndicator({ blockers, warnings }: Readonly<{ blockers: number; warnings: number }>) {
  if (blockers > 0) return <StatusBadge tone="danger">{blockers} 项阻断</StatusBadge>;
  if (warnings > 0) return <StatusBadge tone="warning">{warnings} 项需关注</StatusBadge>;
  return <StatusBadge tone="success">质量正常</StatusBadge>;
}
