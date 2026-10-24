import type { ReactNode } from "react";
import { Drawer } from "antd";

type ContainerProps = { managed: boolean; open: boolean; onClose: () => void; children: ReactNode };

export function TaskRailContainer({ managed, open, onClose, children }: ContainerProps) {
  if (managed) return <>{children}</>;
  // 关闭教师任务抽屉仍保留内部状态，避免已打开的独立重评或筛选被重置。
  return <Drawer title="阅卷任务" open={open} width={420} onClose={onClose} destroyOnHidden={false} className="grading-task-drawer" styles={{ body: { padding: 0 } }}>{children}</Drawer>;
}

export function TaskContentContainer({ managed, open, onClose, children }: ContainerProps) {
  if (!managed) return <>{children}</>;
  return <Drawer title="答卷详情" open={open} width="min(1440px, 96vw)" onClose={onClose} destroyOnHidden className="grading-inspection-drawer">{open ? children : null}</Drawer>;
}
