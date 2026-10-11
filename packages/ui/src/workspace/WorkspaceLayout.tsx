import type { ReactNode } from "react";

/**
 * 让嵌入的阶段模块保持一致的考试上下文，
 * 同时允许每个阶段自行提供领域界面。
 */
export function WorkspaceLayout({
  header,
  stageRail,
  children
}: Readonly<{
  header: ReactNode;
  stageRail: ReactNode;
  children: ReactNode;
}>) {
  return (
    <div className="eg-workspace-layout">
      {header}
      {stageRail}
      {children}
    </div>
  );
}
