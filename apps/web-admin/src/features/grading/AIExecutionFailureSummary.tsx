import { aiFailureSummaryRows } from "./aiExecutionDiagnostics";
import "./ai-execution-diagnostics.css";

export function AIExecutionFailureSummary({ summary }: Readonly<{ summary: unknown }>) {
  const rows = aiFailureSummaryRows(summary);
  if (!rows.length) return null;
  return <ul className="ai-execution-failures" aria-label="AI 失败诊断">
    {rows.map((row) => <li key={row.key}>
      <strong>{row.label} · {row.count} 项</strong>
      <span>{row.attempts} · {row.requests} · 用量：{row.usage}</span>
    </li>)}
  </ul>;
}
