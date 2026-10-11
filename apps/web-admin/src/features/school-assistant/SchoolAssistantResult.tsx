import { Button, Collapse, Tag } from "antd";
import { ArrowUpRight, Database } from "lucide-react";
import type { SchoolAssistantAction, SchoolAssistantResult as Result, SchoolAssistantSource } from "../../api/schoolAssistant";
import type { SessionUser } from "../../auth/session";
import { assistantActionPath } from "./navigation";

const sourceStatus: Record<string, { label: string; color?: string }> = {
  available: { label: "已读取", color: "success" }, partial: { label: "部分可用", color: "warning" },
  empty: { label: "暂无记录" }, unavailable: { label: "读取失败", color: "error" }, forbidden: { label: "无权限", color: "warning" }
};

function readTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "读取时间未知" : date.toLocaleString("zh-CN", { hour12: false });
}

function assistantFactValue(fact: Result["facts"][number]) {
  // 事实不可用时不能展示已有数值；0 是有效值，count 不显示单位文字。
  if (!fact.available || fact.value == null) return "暂不可用";
  const unit = fact.unit === "count" ? "" : fact.unit;
  return `${fact.value} ${unit}`;
}

function AssistantLimitations({ limitations }: Readonly<{ limitations: readonly string[] }>) {
  if (!limitations.length) return null;
  // 限制说明没有独立 ID；用原文和同文出现次数区分，重复说明仍按原顺序逐条保留。
  const occurrences = new Map<string, number>();
  return <ul className="school-assistant-limitations">{limitations.map((item) => {
    const occurrence = occurrences.get(item) ?? 0;
    occurrences.set(item, occurrence + 1);
    return <li key={JSON.stringify([item, occurrence])}>{item}</li>;
  })}</ul>;
}

export function AssistantSources({ sources }: Readonly<{ sources: SchoolAssistantSource[] }>) {
  if (!sources.length) return null;
  return <Collapse ghost className="school-assistant-sources" items={[{
    key: "sources", label: <span className="school-assistant-source-heading"><Database size={15} />查看依据与读取状态（{sources.length}）</span>,
    children: <div>{sources.map((source) => <section key={source.id} className="school-assistant-source">
      <div><strong>{source.label}</strong><Tag color={sourceStatus[source.status]?.color}>{sourceStatus[source.status]?.label ?? source.status}</Tag></div>
      <p>{source.scope_label} · {readTime(source.read_at)}</p>
      {source.revision ? <p>工作区版本：{source.revision}</p> : null}
      {source.release_version != null ? <p>当前发布版本：v{source.release_version}{source.release_id ? ` · ${source.release_id}` : ""}</p> : null}
      {source.note ? <p>{source.note}</p> : null}
    </section>)}<p className="school-assistant-muted">各来源按各自时间读取；正式发布仍在业务页面重新检查。</p></div>
  }]} />;
}

export function SchoolAssistantResult({ result, user, onNavigate }: Readonly<{ result: Result; user: SessionUser; onNavigate: (path: string) => void }>) {
  const validActions = new Map<string, { action: SchoolAssistantAction; path: string }>();
  for (const action of result.actions) {
    const path = assistantActionPath(action, result.scope.exam_id, user);
    if (path) validActions.set(action.id, { action, path });
  }
  const usedActions = new Set(result.blocks.flatMap((block) => (block.rows ?? []).map((row) => row.action_id)));
  return <div className="school-assistant-result">
    <div className="school-assistant-result-scope">{result.scope.scope_label}{result.status === "partial" ? <Tag color="warning">部分来源可用</Tag> : null}</div>
    {result.blocks.map((block, index) => <section key={`${block.type}:${index}`} className={`school-assistant-block is-${block.type}`}>
      {block.title ? <h3>{block.title}</h3> : null}
      {block.text ? <p>{block.text}</p> : null}
      {block.rows?.length ? <ul className="school-assistant-issues">{block.rows.map((row, rowIndex) => {
        const action = row.action_id ? validActions.get(row.action_id) : undefined;
        // 问题代码由业务模块演进；未知代码仍展示服务端事实，只有未识别的导航目标隐藏。
        return <li key={`${row.code}:${rowIndex}`}><div><strong>{row.label}</strong>{row.count != null ? <span>{row.count} {row.unit}</span> : null}</div><p>{row.message}</p>
          {action ? <Button type="link" size="small" icon={<ArrowUpRight size={14} />} onClick={() => onNavigate(action.path)}>{action.action.label}</Button> : null}
        </li>;
      })}</ul> : null}
    </section>)}
    {result.facts.length ? <dl className="school-assistant-facts">{result.facts.map((fact) => <div key={fact.id}><dt>{fact.label}</dt><dd>{assistantFactValue(fact)}</dd>{!fact.available && fact.reason ? <p>{fact.reason}</p> : null}</div>)}</dl> : null}
    <AssistantLimitations limitations={result.limitations} />
    <div className="school-assistant-actions">{[...validActions].filter(([id]) => !usedActions.has(id)).map(([id, item]) => <Button key={id} icon={<ArrowUpRight size={14} />} onClick={() => onNavigate(item.path)}>{item.action.label}</Button>)}</div>
    <AssistantSources sources={result.sources} />
  </div>;
}
