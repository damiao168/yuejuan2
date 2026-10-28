import { Button, List, Select } from "antd";
import { useState } from "react";
import { StatusBadge } from "@edugrade/ui";
import { ChevronDown, ChevronUp, ExternalLink, Trash2 } from "lucide-react";
import type { PaperImportJob, PaperImportRole, PaperImportSource } from "../../api/papers";
import { isPaperImportCancelled, orderedSourcesAfterMove, sourcesAfterRoleChange } from "./materials";
import { importRoleOptions, importSourceDescription, importSourceStatus } from "./paperImportPresentation";
import type { usePaperImportWorkflow } from "./usePaperImportWorkflow";

type SourceWorkflow = Pick<ReturnType<typeof usePaperImportWorkflow>, "updatingImportSources" | "replaceImportSources" | "removeImportSource" | "openImportSource">;
type SourceListProps = Readonly<{ job?: PaperImportJob; workflow: SourceWorkflow }>;

function PaperImportSourceItem({ job, source, index, workflow, editing, onChange }: Readonly<{
  job: PaperImportJob; source: PaperImportSource; index: number; workflow: SourceWorkflow; editing: boolean; onChange: (sources: PaperImportSource[]) => void;
}>) {
  const { updatingImportSources, openImportSource } = workflow;
  const canEditSources = ["processing", "review_required", "failed", "cancelled"].includes(job.status);
  const sourceStatus = importSourceStatus(source, isPaperImportCancelled(job));
  // 呈现层沿用原有状态条件；资料更新及脏草稿拦截仍由 workflow 处理。
  const disabled = !canEditSources || updatingImportSources;
  return <List.Item actions={[
    <Button key="open" type="text" size="small" icon={<ExternalLink size={14} />} onClick={() => void openImportSource(source.file_asset_id)}>来源</Button>,
    editing ? <Button key="up" type="text" size="small" aria-label="上移资料" icon={<ChevronUp size={14} />} disabled={index === 0 || disabled} onClick={() => onChange(orderedSourcesAfterMove(job.sources, source.id, -1))} /> : null,
    editing ? <Button key="down" type="text" size="small" aria-label="下移资料" icon={<ChevronDown size={14} />} disabled={index === job.sources.length - 1 || disabled} onClick={() => onChange(orderedSourcesAfterMove(job.sources, source.id, 1))} /> : null,
    editing ? <Select<PaperImportRole> key="role" size="small" aria-label="资料类型" value={source.role_hint} options={importRoleOptions} disabled={disabled} onChange={(roleHint) => onChange(sourcesAfterRoleChange(job.sources, source.id, roleHint))} /> : null,
    editing ? <Button key="remove" type="text" danger size="small" aria-label="删除资料" icon={<Trash2 size={14} />} disabled={disabled} onClick={() => onChange(job.sources.filter(item => item.id !== source.id).map((item, document_index) => ({ ...item, document_index })))} /> : null
  ]} extra={<StatusBadge tone={sourceStatus.tone}>{sourceStatus.label}</StatusBadge>}>
    <List.Item.Meta title={(source.document_index + 1) + ". " + (source.original_name || "考试资料")} description={importSourceDescription(source)} />
  </List.Item>;
}

export function PaperImportSourceList({ job, workflow }: SourceListProps) {
  if (!job?.sources.length) return null;
  return <EditableSources key={`${job.id}:${job.generation}:${job.source_revision}`} job={job} workflow={workflow} />;
}

function EditableSources({ job, workflow }: Readonly<{ job: PaperImportJob; workflow: SourceWorkflow }>) {
  const [editing, setEditing] = useState(false);
  const [sources, setSources] = useState(job.sources);
  const changed = JSON.stringify(sources) !== JSON.stringify(job.sources);
  const current = { ...job, sources: editing ? sources : job.sources };
  return <>
    {/* 本地排序和类型一起提交原代次；新代次重建草稿，未保存的逐题编辑仍由工作流拦截。 */}
    <List size="small" className="paper-import-source-list" dataSource={current.sources}
      renderItem={(source, index) => <PaperImportSourceItem job={current} source={source} index={index} workflow={workflow} editing={editing} onChange={setSources} />} />
    {editing ? <><Button type="primary" disabled={!changed || workflow.updatingImportSources} loading={workflow.updatingImportSources} onClick={async () => { if (await workflow.replaceImportSources(job, sources)) setEditing(false); }}>保存资料调整并匹配</Button><Button disabled={workflow.updatingImportSources} onClick={() => { setSources(job.sources); setEditing(false); }}>取消资料调整</Button></>
      : ["processing", "review_required", "failed", "cancelled"].includes(job.status) ? <Button disabled={workflow.updatingImportSources} onClick={() => { setSources(job.sources); setEditing(true); }}>调整资料</Button> : null}
  </>;
}
