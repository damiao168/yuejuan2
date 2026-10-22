import { useEffect, useRef, useState } from "react";
import { Button, Checkbox, Modal, Space } from "antd";
import { getUserErrorMessage } from "../../api/client";
import { getPaperImport, registerPaperFromFile, type PaperImportJob, type PaperVersion } from "../../api/papers";

type ConfirmationState = {
  owner: number;
  open: boolean;
  checked: boolean;
  busy: boolean;
  readPending: boolean;
  error: string;
};

function initialState(owner: number): ConfirmationState {
  return { owner, open: false, checked: false, busy: false, readPending: false, error: "" };
}

function structureConfirmed(job: PaperImportJob) {
  return job.status === "applied" || job.status === "review_required" && job.generation > 0
    && job.structure_confirmed_generation === job.generation;
}

function sourceSnapshot(job: PaperImportJob) {
  return JSON.stringify(job.sources.map((source) => [source.id, source.file_asset_id, source.document_index,
    source.role_hint, source.detected_role, source.processing_status]));
}

function questionSources(job: PaperImportJob) {
  const sources = job.sources.filter((source) => source.detected_role === "question" || source.detected_role === "mixed");
  const unique = new Map(sources.map((source) => [source.file_asset_id, source]));
  return { sources: [...unique.values()], valid: sources.length > 0 && sources.every((source) => source.processing_status === "processed" && !!source.file_asset_id) };
}

function registeredQuestionAssets(job: PaperImportJob, papers: PaperVersion[]) {
  return new Set(papers.filter((paper) => paper.exam_id === job.exam_id
    && (!paper.purpose || paper.purpose === "question_paper") && paper.status !== "deleted").map((paper) => paper.file_asset_id));
}

class MaterialsChangedError extends Error {}

function materialsDescription(confirmed: boolean, valid: boolean, completed: boolean) {
  if (!confirmed) return "请先预览并采用本轮完整试卷的题目结构，再确认考试资料。";
  if (!valid) return "没有完整识别的试题资料来源，请补充或重新识别试卷；答案、解析资料不能代替试卷。";
  if (completed) return "试卷题目资料已登记；题目分值、答案和评分配置仍以小题与分值中的正式配置为准。";
  return "请核对试卷资料完整后确认；答案、分值和评分配置在小题与分值中继续核对。";
}

function materialsBlockedReason(confirmed: boolean, valid: boolean, canManage: boolean, disabled: boolean, description: string, disabledReason?: string) {
  if (!confirmed || !valid) return description;
  if (!canManage) return "当前考试或账号只允许查看，不能确认考试资料。";
  return disabled ? disabledReason || "请先完成并保存当前修改或资料处理，再确认考试资料。" : "";
}

function materialsStatus(state: ConfirmationState, confirmed: boolean, valid: boolean, completed: boolean) {
  let tone = completed ? "success" : "info";
  if (state.error || !confirmed || !valid) tone = "warning";
  if (state.readPending) return { tone, text: completed ? "资料已登记，等待状态刷新" : "考试资料状态待刷新" };
  return { tone, text: completed ? "资料已登记" : "资料待确认" };
}

/** 采用结构后可复用本流程补登记；先重新核对代次与来源，不把旧预览当作当前确认。 */
export async function registerConfirmedPaperMaterials(
  job: PaperImportJob, papers: PaperVersion[], isCurrent: () => boolean,
  onRegistered?: (paper: PaperVersion) => void,
): Promise<void> {
  const assertCurrent = () => { if (!isCurrent()) throw new Error("确认操作已暂停，请重新核对当前考试资料。"); };
  assertCurrent();
  const fresh = (await getPaperImport(job.id)).import;
  assertCurrent();
  if (fresh.id !== job.id || fresh.exam_id !== job.exam_id || fresh.generation !== job.generation
    || fresh.source_revision !== job.source_revision || sourceSnapshot(fresh) !== sourceSnapshot(job) || !structureConfirmed(fresh)) {
    throw new MaterialsChangedError("考试资料已变化，请读取最新结果后重新核对。");
  }
  const { sources, valid } = questionSources(fresh);
  if (!valid) throw new Error("没有完整识别的试题资料来源，不能确认考试资料。");
  const registered = registeredQuestionAssets(job, papers);
  // 逐份登记；后续请求失败不会撤销成功回执，服务端也按考试与文件复用已有登记。
  for (const source of sources) {
    assertCurrent();
    if (registered.has(source.file_asset_id)) continue;
    const { paper } = await registerPaperFromFile(job.exam_id, source.file_asset_id);
    registered.add(source.file_asset_id);
    onRegistered?.(paper);
    assertCurrent();
  }
}

/** 结构采用与资料登记是两个事实；这里只登记实际试题来源，不改题目、答案或分值。 */
export function PaperMaterialsConfirmation({ job, papers, canManage, disabled, disabledReason, onConfirmed }: Readonly<{
  job: PaperImportJob;
  papers: PaperVersion[];
  canManage: boolean;
  disabled: boolean;
  disabledReason?: string;
  onConfirmed: () => Promise<void>;
}>) {
  const snapshot = sourceSnapshot(job);
  const key = `${job.exam_id}:${job.id}:${job.generation}:${job.source_revision}:${snapshot}`;
  const scope = useRef({ key, version: 0, receipts: new Map<string, PaperVersion>() });
  if (scope.current.key !== key) scope.current = { key, version: scope.current.version + 1, receipts: new Map<string, PaperVersion>() };
  const version = scope.current.version;
  const latest = useRef({ canManage, disabled, onConfirmed });
  latest.current = { canManage, disabled, onConfirmed };
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; };
  }, []);
  const operation = useRef<number | null>(null);
  const [storedState, setStoredState] = useState(() => initialState(version));
  const state = storedState.owner === version ? storedState : initialState(version);
  const { sources, valid } = questionSources(job);
  const registered = registeredQuestionAssets(job, papers);
  for (const assetId of scope.current.receipts.keys()) registered.add(assetId);
  const pending = sources.filter((source) => !registered.has(source.file_asset_id));
  const confirmed = structureConfirmed(job);
  const completed = confirmed && valid && pending.length === 0;
  const owns = () => mounted.current && scope.current.version === version;
  const canWrite = () => owns() && latest.current.canManage && !latest.current.disabled;
  const patch = (next: Partial<ConfirmationState>) => {
    if (!owns()) return;
    setStoredState((current) => ({ ...(current.owner === version ? current : initialState(version)), ...next }));
  };

  async function readConfirmation() {
    // 写入成功而回读失败时只重读；不能再次登记资料，更不能重新采用题目结构。
    patch({ readPending: true });
    try {
      await latest.current.onConfirmed();
      patch({ readPending: false, error: "", open: false, checked: false });
    } catch (error) {
      patch({ readPending: true, open: false, error: `考试资料状态尚未重新读取，请重试读取。${getUserErrorMessage(error, "读取失败")}` });
    }
  }

  async function reread() {
    if (!owns() || operation.current === version) return;
    operation.current = version;
    patch({ busy: true });
    try { await readConfirmation(); }
    finally {
      if (operation.current === version) operation.current = null;
      patch({ busy: false });
    }
  }

  async function confirm() {
    if (!canWrite() || !confirmed || !valid || !state.checked || state.readPending || operation.current === version) return;
    // ref 先占用本次确认，连续点击和旧弹窗回调不能并发登记；换考试后旧回执不能影响新页面。
    operation.current = version;
    patch({ busy: true, error: "" });
    try {
      await registerConfirmedPaperMaterials(job, [...papers, ...scope.current.receipts.values()], canWrite,
        (paper) => { if (owns()) scope.current.receipts.set(paper.file_asset_id, paper); });
      if (!canWrite()) return;
      patch({ open: false, checked: false });
      await readConfirmation();
    } catch (error) {
      if (!owns()) return;
      if (error instanceof MaterialsChangedError) {
        patch({ open: false, checked: false, error: error.message });
        await readConfirmation();
        return;
      }
      const savedCount = sources.filter((source) => registered.has(source.file_asset_id) || scope.current.receipts.has(source.file_asset_id)).length;
      patch({ error: `已登记 ${savedCount} / ${sources.length} 份试卷资料，其余登记结果待核实；可重试确认，已登记的资料会保留。${getUserErrorMessage(error, "确认失败")}` });
    } finally {
      if (operation.current === version) operation.current = null;
      patch({ busy: false });
    }
  }

  const description = materialsDescription(confirmed, valid, completed);
  const blockedReason = materialsBlockedReason(confirmed, valid, canManage, disabled, description, disabledReason);
  const status = materialsStatus(state, confirmed, valid, completed);
  return <div className="paper-material-confirmation" aria-label="考试资料登记">
    <div className="paper-material-status-row">
      <span className="paper-check-status" data-tone={status.tone} role="status">
        {status.text}
      </span>
      {state.readPending && <Button size="small" loading={state.busy} onClick={() => void reread()}>重新读取确认结果</Button>}
      {!state.readPending && !completed && <Button type="primary" disabled={!canManage || disabled || !confirmed || !valid || state.busy}
        onClick={() => patch({ open: true, checked: false, error: "" })}>确认考试资料</Button>}
      <details className="paper-material-details"><summary>查看确认详情</summary><p>{description}</p></details>
    </div>
    {!completed && blockedReason && <p className="paper-check-explanation">{blockedReason}</p>}
    {state.error && <p className="paper-check-explanation" role="alert">{state.error}</p>}
    <Modal title="确认考试资料" open={state.open} confirmLoading={state.busy} okText="确认考试资料"
      onCancel={() => { if (!state.busy) patch({ open: false, checked: false }); }}
      onOk={() => void confirm()} okButtonProps={{ disabled: !canManage || disabled || !state.checked || !confirmed || !valid || state.readPending }} destroyOnHidden>
      <Space direction="vertical" style={{ width: "100%" }}>
        <p>请核对这些试卷资料完整，题目分值在下一步设置。</p>
        <ul>{pending.map((source) => <li key={source.file_asset_id}>{source.original_name || `试卷资料 ${source.document_index + 1}`}</li>)}</ul>
        <Checkbox checked={state.checked} disabled={state.busy} onChange={(event) => patch({ checked: event.target.checked })}>
          我已核对试卷资料完整，题目范围与实际试卷一致
        </Checkbox>
        {state.error && <p role="alert">{state.error}</p>}
      </Space>
    </Modal>
  </div>;
}
