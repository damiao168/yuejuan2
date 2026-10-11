import type { ReactNode } from "react";

/** 为现有 Ant Design 筛选控件提供响应式容器，不限定筛选方式。 */
export function FilterBar({ children, label = "筛选条件" }: Readonly<{ children: ReactNode; label?: string }>) {
  return <section className="eg-filter-bar" aria-label={label}>{children}</section>;
}
