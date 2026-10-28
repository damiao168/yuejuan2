import { Button, Collapse } from "antd";
import { ArrowUpRight, Check, Database, LoaderCircle, TriangleAlert } from "lucide-react";
import type { SessionUser } from "../../auth/session";
import type { ConversationEvidence } from "./conversationState";
import { assistantActionPath } from "./navigation";

const statusLabel: Record<string, string> = { available: "已读取", partial: "部分可用", empty: "暂无记录", unavailable: "读取失败", forbidden: "无权限" };

function ConversationToolItem({ tool }: Readonly<{ tool: ConversationEvidence["tools"][number] }>) {
  let icon = <Check size={13} />;
  let label = "已查询";
  if (tool.status === "running") {
    icon = <LoaderCircle size={13} />;
    label = "正在查询";
  } else if (tool.status === "failed") {
    icon = <TriangleAlert size={13} />;
    label = tool.message || "查询失败";
  }
  return <li className={`is-${tool.status}`}>
    {icon}<span>{tool.label}</span><small>{label}</small>
  </li>;
}

export function ConversationEvidenceView({ evidence, user, onNavigate, allowActions = true }: Readonly<{
  evidence?: ConversationEvidence; user: SessionUser; onNavigate?: (path: string) => void; allowActions?: boolean;
}>) {
  if (!evidence || (!evidence.tools.length && !evidence.sources.length)) return null;
  const running = evidence.tools.some(tool => tool.status === "running");
  const failed = evidence.tools.filter(tool => tool.status === "failed");
  const sourceIncomplete = evidence.sources.some(source => ["partial", "unavailable", "forbidden"].includes(source.status));
  return <div className="school-conversation-evidence">
    {running ? <ul className="school-conversation-tools" aria-label="业务查询进度">{evidence.tools.map((tool) => <ConversationToolItem key={tool.call_id} tool={tool} />)}</ul> : <>
      {evidence.tools.length ? <span>已查询{evidence.tools.length - failed.length}项资料</span> : null}
      {failed.length ? <ul className="school-conversation-tools" aria-label="查询失败">{failed.map(tool => <ConversationToolItem key={tool.call_id} tool={tool} />)}</ul> : null}
    </>}
    {/* 完成后只收拢成功过程；缺失来源仍直接提醒，不能靠折叠来源区掩盖回答范围。 */}
    {failed.length || sourceIncomplete ? <p role="status">部分资料未能完整读取，请核对回答范围。</p> : null}
    {evidence.sources.length ? <Collapse ghost size="small" className="school-conversation-sources" items={[{
      key: "sources", label: <span><Database size={13} />数据来源（{evidence.sources.length}）</span>,
      children: <div>{evidence.sources.map((source) => {
        const date = new Date(source.read_at);
        return <section key={source.id}><strong>{source.label}</strong><span>{statusLabel[source.status] ?? source.status}</span>
          <p>{source.scope_label} · {Number.isNaN(date.getTime()) ? "读取时间未知" : date.toLocaleString("zh-CN", { hour12: false })}</p>
          {source.revision ? <p>工作区版本 {source.revision}</p> : null}
          {source.release_version != null ? <p>生效发布版本 v{source.release_version}</p> : null}
          {source.note ? <p>{source.note}</p> : null}
        </section>;
      })}</div>
    }]} /> : null}
    {allowActions && onNavigate ? <div className="school-conversation-actions">{evidence.actions.flatMap((action) => {
      // 只接受后端附带的受控业务目标；模型正文里的 URL 不会变成操作按钮。
      const path = assistantActionPath(action, action.target.exam_id, user);
      return path ? [<Button key={action.id} size="small" icon={<ArrowUpRight size={13} />} onClick={() => onNavigate(path)}>{action.label}</Button>] : [];
    })}</div> : null}
  </div>;
}
