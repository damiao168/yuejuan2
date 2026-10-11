export interface WorkspaceStage {
  key: string;
  label: string;
  // 工作区投影允许扩展状态，通用组件不能把服务端新值收窄为固定枚举。
  state: string;
  action_route: string;
  summary?: string;
}

export interface WorkspaceNotice {
  code: string;
  title: string;
  message: string;
  severity: string;
  action_label?: string;
  action_route?: string;
}

export interface WorkspaceMetric {
  key: string;
  label: string;
  value: number | string;
  suffix?: string;
  helper?: string;
}
