import { Alert, Button, Drawer, Empty, List } from "antd";
import type { ReactNode } from "react";
import type { ScoreRelease } from "../../../api/scoreReleases";
import { StatusTag } from "../../../components/StatusTag";
import { formatReleaseTime } from "../releaseStatus";

const sourceLabels: Record<string, string> = {
  initial: "首次发布",
  regrade: "复评更正",
  appeal: "申诉更正",
  rollback: "回退版本",
  migration: "历史迁移"
};

function statusLabel(release: ScoreRelease, current?: ScoreRelease) {
  if (current?.id === release.id) return { tone: "success" as const, label: "学生端当前版本" };
  if (release.status === "published") return { tone: "neutral" as const, label: "历史版本" };
  return { tone: "processing" as const, label: "草稿 · 学生看不到" };
}

/**
 * 发布记录。版本、草稿等技术概念只在这里出现；正常发布走页面上方的一个主按钮。
 * 学生端以服务端“当前版本”指针为准，较早生成的草稿也可能最后发布。
 */
export function ScoreReleaseHistory({ open, onClose, releases, currentRelease, canWrite, canPublish, actioning, onPreview, onPublish, onRevokePaper, onRepublish }: Readonly<{
  open: boolean;
  onClose: () => void;
  releases: ScoreRelease[];
  currentRelease?: ScoreRelease;
  canWrite: boolean;
  /** 发布前检查通过；未通过时草稿不能发布。 */
  canPublish: boolean;
  actioning: string | null;
  onPreview: (release: ScoreRelease) => void;
  onPublish: (release: ScoreRelease) => void;
  onRevokePaper: (release: ScoreRelease) => void;
  onRepublish: () => void;
}>) {
  const ordered = [...releases].sort((a, b) => b.version - a.version);
  const hasDraft = releases.some(release => release.status === "draft");
  return <Drawer
    title={`发布记录（${releases.length}）`}
    width="min(720px, 96vw)"
    open={open}
    onClose={onClose}
    extra={currentRelease ? <Button disabled={!canWrite || !canPublish} onClick={onRepublish}>按当前成绩重新发布</Button> : null}
  >
    {hasDraft && !canPublish ? <Alert className="score-release-history-note" type="info" showIcon message="发布前检查尚未通过，暂不能发布草稿。" description="请先处理页面上方列出的待办。" /> : null}
    {ordered.length ? <List
      size="small"
      dataSource={ordered}
      renderItem={(release) => {
        const status = statusLabel(release, currentRelease);
        const actions: ReactNode[] = [<Button key="preview" size="small" disabled={!canWrite} onClick={() => onPreview(release)}>查看学生视角</Button>];
        if (release.status === "draft") {
          actions.push(<Button key="publish" size="small" disabled={!canWrite || !canPublish} loading={actioning === "release-publish"} onClick={() => onPublish(release)}>发布此版本</Button>);
        } else if (currentRelease?.id === release.id && release.visibility_policy.show_high_score_paper) {
          actions.push(<Button key="revoke-paper" size="small" danger disabled={!canWrite} loading={actioning === "release-high-score-revoke"} onClick={() => onRevokePaper(release)}>撤回范例卷分享</Button>);
        }
        const published = formatReleaseTime(release.published_at);
        const created = formatReleaseTime(release.created_at);
        return <List.Item actions={actions}>
          <div className="score-release-row">
            <div>
              <strong>V{release.version} · {sourceLabels[release.source] ?? "其他来源"}</strong>
              <span>{release.reason}</span>
              <span>{published ? `发布于 ${published}` : created ? `生成于 ${created}` : ""}</span>
            </div>
            <StatusTag tone={status.tone}>{status.label}</StatusTag>
          </div>
        </List.Item>;
      }}
    /> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有发布过成绩。检查通过后，点页面上方的「发布成绩」。" />}
  </Drawer>;
}
