import type { WorkspaceMetric } from "./types";

export function MetricCard({ metric }: Readonly<{ metric: WorkspaceMetric }>) {
  return (
    <div className="eg-workspace-metric">
      <span>{metric.label}</span>
      <strong>{metric.value}{metric.suffix ? <small>{metric.suffix}</small> : null}</strong>
      {metric.helper ? <p>{metric.helper}</p> : null}
    </div>
  );
}
