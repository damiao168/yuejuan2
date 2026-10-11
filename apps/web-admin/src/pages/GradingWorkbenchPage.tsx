import { GradingWorkbench, type GradingWorkbenchProps } from "../features/grading/workbench/GradingWorkbench";

/**
 * 阅卷工作区在路由层的组合外壳。
 *
 * 操作状态、任务队列、预览控件和评分面板都放在阅卷功能中，
 * 使该路由保持稳定的权限边界。
 */
export function GradingWorkbenchPage(props: Readonly<GradingWorkbenchProps>) {
  return <GradingWorkbench {...props} />;
}
