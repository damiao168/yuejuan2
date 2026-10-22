import { Button } from "antd";

export type GradingManagementView = "overview" | "results" | "assignment" | "progress" | "settings" | "work";

interface GradingManagementNavProps {
  view: GradingManagementView;
  hasExamOverview: boolean;
  canViewResults: boolean;
  canOpenResults: boolean;
  resultsOpen: boolean;
  resultsLoading: boolean;
  hasOwnWork: boolean;
  canManageSettings?: boolean;
  onViewChange: (view: GradingManagementView) => void;
  onOpenResults: () => void;
}

export function GradingManagementNav({ view, hasExamOverview, canViewResults, canOpenResults, resultsLoading, hasOwnWork, canManageSettings = false, onViewChange, onOpenResults }: Readonly<GradingManagementNavProps>) {
  const viewButton = (value: GradingManagementView, label: string) => <Button key={value} type="text" className={view === value ? "is-active" : undefined} aria-current={view === value ? "page" : undefined} onClick={() => onViewChange(value)}>{label}</Button>;
  return <nav className="grading-management-nav" aria-label="阅卷工作区">
    {hasExamOverview ? viewButton("overview", "阅卷概览") : null}
    {canViewResults ? <Button type="text" className={view === "results" ? "is-active" : undefined} aria-current={view === "results" ? "page" : undefined} disabled={!canOpenResults} title={!canOpenResults ? "开始评分后可查看答卷结果" : undefined} loading={view === "results" && resultsLoading} onClick={onOpenResults}>答卷结果</Button> : null}
    {viewButton("assignment", "安排教师")}
    {viewButton("progress", "教师进度")}
    {hasOwnWork ? viewButton("work", "我的阅卷") : null}
    {canManageSettings ? <div className="grading-management-settings">{viewButton("settings", "阅卷设置")}</div> : null}
  </nav>;
}
