import { Button, List, Select } from "antd";
import { StatusBadge } from "@edugrade/ui";
import { ChevronDown, ChevronUp, ExternalLink, Trash2 } from "lucide-react";
import type { PaperImportJob, PaperImportRole, PaperImportSource } from "../../api/papers";
import { isPaperImportCancelled, orderedSourcesAfterMove, sourcesAfterRoleChange } from "./materials";
import { importRoleOptions, importSourceDescription, importSourceStatus } from "./paperImportPresentation";
import type { usePaperImportWorkflow } from "./usePaperImportWorkflow";

type SourceWorkflow = Pick<ReturnType<typeof usePaperImportWorkflow>, "updatingImportSources" | "replaceImportSources" | "removeImportSource" | "openImportSource">;
type SourceListProps = Readonly<{ job?: PaperImportJob; workflow: SourceWorkflow }>;

function PaperImportSourceItem({ job, source, index, workflow }: Readonly<{
  job: PaperImportJob; source: PaperImportSource; index: number; workflow: SourceWorkflow;
}>) {
  const { updatingImportSources, replaceImportSources, removeImportSource, openImportSource } = workflow;
  const canEditSources = ["processing", "review_required", "failed", "cancelled"].includes(job.status);
  const sourceStatus = importSourceStatus(source, isPaperImportCancelled(job));
  // 呈现层沿用原有状态条件；资料更新及脏草稿拦截仍由 workflow 处理。
  const disabled = !canEditSources || updatingImportSources;
  return <List.Item actions={[
    <Button key="open" type="text" size="small" icon={<ExternalLink size={14} />} onClick={() => void openImportSource(source.file_asset_id)}>来源</Button>,
    <Button key="up" type="text" size="small" aria-label="上移资料" icon={<ChevronUp size={14} />} disabled={index === 0 || disabled} onClick={() => void replaceImportSources(job, orderedSourcesAfterMove(job.sources, source.id, -1))} />,
    <Button key="down" type="text" size="small" aria-label="下移资料" icon={<ChevronDown size={14} />} disabled={index === job.sources.length - 1 || disabled} onClick={() => void replaceImportSources(job, orderedSourcesAfterMove(job.sources, source.id, 1))} />,
    <Select<PaperImportRole> key="role" size="small" aria-label="资料类型" value={source.role_hint} options={importRoleOptions} disabled={disabled} onChange={(roleHint) => void replaceImportSources(job, sourcesAfterRoleChange(job.sources, source.id, roleHint))} />,
    <Button key="remove" type="text" danger size="small" aria-label="删除资料" icon={<Trash2 size={14} />} disabled={disabled} onClick={() => void removeImportSource(job, source.id)} />
  ]} extra={<StatusBadge tone={sourceStatus.tone}>{sourceStatus.label}</StatusBadge>}>
    <List.Item.Meta title={(source.document_index + 1) + ". " + (source.original_name || "考试资料")} description={importSourceDescription(source)} />
  </List.Item>;
}

export function PaperImportSourceList({ job, workflow }: SourceListProps) {
  if (!job?.sources.length) return null;
  return <List size="small" className="paper-import-source-list" dataSource={job.sources}
    renderItem={(source, index) => <PaperImportSourceItem job={job} source={source} index={index} workflow={workflow} />} />;
}
