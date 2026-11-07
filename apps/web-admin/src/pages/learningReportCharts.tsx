import { Alert, Segmented } from "antd";
import { type ReactNode } from "react";
import { Bar, BarChart, CartesianGrid, Tooltip as ChartTooltip, ResponsiveContainer, XAxis, YAxis } from "recharts";
import { type OverviewReport } from "../api/reports";
import { EmptyState, ErrorState } from "../components/PageState";
import { emptyReason } from "./learningReports.model";


export type ClassMetric = "average" | "passRate";
export interface ClassComparisonChartEntry {
  name: string;
  average: number;
  median: number;
  passRate: number;
  excellentRate: number;
  studentCount: number;
}

export const chartBlue = "#1677ff";
export const chartGreen = "#52c41a";
export const chartAmber = "#faad14";
export const chartRed = "#ff4d4f";
export const chartCyan = "#13c2c2";

export function ChartPanel({
  title,
  description,
  empty,
  emptyTitle,
  children,
  className = ""
}: Readonly<{
  title: string;
  description: string;
  empty: boolean;
  emptyTitle: string;
  children: ReactNode;
  className?: string;
}>) {
  return (
    <section className={`reports-panel ${className}`}>
      <div className="panel-head">
        <div>
          <h2>{title}</h2>
          <p>{description}</p>
        </div>
      </div>
      {empty ? <EmptyState title={emptyTitle} description="暂无数据，成绩发布后自动生成。" /> : <div className="reports-chart">{children}</div>}
    </section>
  );
}

export function renderClassComparisonChart(classMetric: ClassMetric, classComparisonData: ClassComparisonChartEntry[], setClassMetric: (metric: ClassMetric) => void) {
  return <ChartPanel className="reports-view-classes" title="班级对比" description={classMetric === "average" ? "按平均分比较，使用同一分数刻度。" : "按及格率比较，使用同一百分比刻度。"} empty={classComparisonData.length === 0} emptyTitle="暂无班级对比">
    <Segmented value={classMetric} options={[{ value: "average", label: "平均分" }, { value: "passRate", label: "及格率" }]} onChange={(next) => setClassMetric(next as ClassMetric)} />
    <ResponsiveContainer width="100%" height={260}>
      <BarChart data={classComparisonData}>
        <CartesianGrid strokeDasharray="3 3" vertical={false} />
        <XAxis dataKey="name" />
        <YAxis domain={classMetric === "passRate" ? [0, 100] : undefined} label={{ value: classMetric === "average" ? "分数" : "百分比", angle: -90, position: "insideLeft" }} />
        <ChartTooltip />
        <Bar dataKey={classMetric} name={classMetric === "average" ? "平均分" : "及格率%"} fill={classMetric === "average" ? chartBlue : chartGreen} radius={[4, 4, 0, 0]} />
      </BarChart>
    </ResponsiveContainer>
  </ChartPanel>;
}

export function LearningReportNotices({ canRead, error, empty, onRetry }: Readonly<{
  canRead: boolean;
  error: string | null;
  empty: OverviewReport["empty"];
  onRetry: () => Promise<void>;
}>) {
  // 权限、请求失败和空报告是独立提示；抽出展示后仍可同时出现，不改变父层请求与重试流程。
  return <>
    {!canRead ? <Alert type="error" showIcon message="无报告查看权限" description="当前账号没有查看学情报告的权限，请联系管理员开通。" /> : null}
    {error ? <ErrorState message={error} onRetry={onRetry} /> : null}
    {empty?.empty ? <Alert type="info" showIcon message="报告为空" description={emptyReason(empty.reason)} /> : null}
  </>;
}
