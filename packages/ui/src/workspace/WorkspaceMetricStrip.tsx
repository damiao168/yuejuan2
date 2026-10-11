import type { WorkspaceMetric } from "./types";
import { MetricCard } from "./MetricCard";

export function WorkspaceMetricStrip({ metrics }: Readonly<{ metrics: WorkspaceMetric[] }>) {
  return (
    <section className="eg-workspace-metrics" aria-label="考试关键数据">
      {metrics.map((metric) => (
        <MetricCard key={metric.key} metric={metric} />
      ))}
    </section>
  );
}
