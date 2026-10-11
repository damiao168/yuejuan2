import type { ReactNode } from "react";

export type StatusBadgeTone = "neutral" | "info" | "success" | "warning" | "danger" | "processing";

/** 表达状态语义，不再抽象一套按钮或输入框。 */
export function StatusBadge({ tone = "neutral", children }: Readonly<{ tone?: StatusBadgeTone; children: ReactNode }>) {
  return <span className={`eg-status-badge is-${tone}`}>{children}</span>;
}
