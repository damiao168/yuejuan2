import { Alert, Button } from "antd";
import { getSafeUserText } from "../../../api/client";
import { publicationCheckPresentation, publicationReadiness, type PublicationCheckItem } from "../publicationChecklist";
import type { ScoreReleaseGate } from "../../../api/scoreReleases";
import type { QualityCheckResult, RosterReport } from "../../../api/scores";

function CheckDetails({ items }: Readonly<{ items: PublicationCheckItem[] }>) {
  return <ul className="score-publication-check-reasons">{items.map(item => {
    const presentation = publicationCheckPresentation(item);
    const situation = getSafeUserText(presentation.situation, "当前发布条件尚未满足，请刷新后核对。");
    // 来源可能重复报同一中文原因；只保留不同说明，不把内部检查代码带到详情。
    const messages = [...new Set([situation, ...item.sources.map(source => source.message)]
      .map(message => getSafeUserText(message.replace(/\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b/gi, ""), "").trim()).filter(Boolean))];
    return <li key={item.code}><strong>{presentation.label}</strong>{messages.map(message => <p key={message}>{message}</p>)}</li>;
  })}</ul>;
}

export function ScorePublicationChecklist({ quality, gate, roster, actionFor }: {
  quality: QualityCheckResult | null; gate: ScoreReleaseGate | null; roster: RosterReport | null;
  actionFor: (code: string, route?: string) => { label: string; run: () => void } | undefined;
}) {
  // 是否可以发布由状态行统一说明；这里只列出要处理的环节和不影响发布的提醒。
  const { status, groups, reminders } = publicationReadiness(quality, gate, roster);
  if (status === "ready" && reminders.length === 0) return null;
  const headline = { loading: "正在核对", blocked: `${groups.length} 个环节待完成`, unexplained: "检查尚未通过", ready: "检查已通过" }[status];
  return <section className="score-publication-checklist" id="score-publication-checklist" tabIndex={-1} aria-label="发布待办">
    <div className="panel-head"><h3>发布前检查</h3><span className="muted">{headline}</span></div>
    {status === "loading" ? <Alert type="info" showIcon message="检查尚未读取完整，暂不能确认发布条件。" /> : null}
    {groups.length ? <ol className="score-publication-task-groups">{groups.map(group => {
      // 同一环节只提供一个入口，优先处理排序靠前且当前账号可执行的问题。
      const action = group.items.map(item => actionFor(item.code, item.actionRoute)).find(Boolean);
      return <li key={group.key} className="score-publication-task-group"><div className="score-publication-task-heading"><strong>{group.label}</strong><span className="muted">{group.items.length} 项检查</span></div><div className="score-publication-task-summary">{group.summary}</div><div className="score-publication-task-action">{action ? <Button size="small" onClick={action.run}>{action.label}</Button> : <span className="muted">请联系考试管理员处理</span>}</div><details className="score-publication-task-details"><summary>查看详情</summary><CheckDetails items={group.items} /></details></li>;
    })}</ol> : null}
    {status === "unexplained" ? <Alert type="warning" showIcon message="当前检查尚未通过，但没有返回具体原因。" description="请刷新后再看；仍未通过时，请联系系统管理员核对发布检查。" /> : null}
    {reminders.length ? <details className="score-publication-reminders"><summary>其他提醒 · {reminders.length} 项<span className="muted">不影响发布</span></summary><CheckDetails items={reminders} /></details> : null}
  </section>;
}
