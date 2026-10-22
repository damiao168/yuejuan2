import { useEffect, useRef, useState } from "react";
import { Alert, App, Button, Checkbox, Modal, Space, Table } from "antd";
import {
  adoptPaperStructure,
  previewPaperStructure,
  type PaperImportJob,
  type PaperVersion,
  type PaperStructurePreview,
} from "../../api/papers";
import { getUserErrorMessage } from "../../api/client";
import { questionTypeOptions } from "../../constants/examCatalog";
import { registerConfirmedPaperMaterials } from "./PaperMaterialsConfirmation";

const actions: Record<string, string> = {
  add: "新增",
  remove: "移除空白占位",
  update: "调整",
  keep: "保留",
  conflict: "需单独处理",
};
const typeName = (value?: string) => questionTypeOptions.find((item) => item.value === value)?.label ?? value ?? "—";

export function PaperStructurePanel({
  job,
  disabled,
  papers = [],
  onAdopted,
}: Readonly<{
  job: PaperImportJob;
  disabled: boolean;
  papers?: PaperVersion[];
  onAdopted: () => Promise<void>;
}>) {
  const { message } = App.useApp();
  const [preview, setPreview] = useState<PaperStructurePreview | null>(null);
  const [error, setError] = useState("");
  const [open, setOpen] = useState(false);
  const [complete, setComplete] = useState(false);
  const [saving, setSaving] = useState(false);
  const key = `${job.id}:${job.generation}:${job.updated_at}`;
  const active = useRef({ key, version: 0 });
  if (active.current.key !== key) active.current = { key, version: active.current.version + 1 };
  const version = active.current.version;
  const mounted = useRef(true);
  const unavailable = useRef(disabled);
  unavailable.current = disabled;
  const savingScope = useRef<number | null>(null);
  const isCurrent = () => mounted.current && active.current.key === key && active.current.version === version;
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    const controller = new AbortController();
    setPreview(null);
    setError("");
    setOpen(false);
    setComplete(false);
    setSaving(false);
    // 资料代次或核对内容变化后丢弃旧预览；卸载后的请求不能更新另一份资料的页面。
    void previewPaperStructure(job.id, controller.signal)
      .then((result) => {
        if (!controller.signal.aborted && active.current.key === key && active.current.version === version) setPreview(result);
      })
      .catch((err) => {
        if (!controller.signal.aborted && active.current.key === key && active.current.version === version) setError(getUserErrorMessage(err, "结构预览加载失败"));
      });
    return () => controller.abort();
  }, [key, job.id, version]);
  async function refreshAndOpen() {
    if (unavailable.current || !isCurrent() || savingScope.current === version) return;
    try {
      const current = await previewPaperStructure(job.id);
      if (!isCurrent() || unavailable.current) return;
      setPreview(current);
      setError("");
      setComplete(false);
      setOpen(true);
    } catch (err) {
      if (isCurrent()) message.error(getUserErrorMessage(err, "结构预览加载失败"));
    }
  }
  async function adopt() {
    if (!preview || preview.blocked_reasons.length > 0 || !complete || unavailable.current || !isCurrent() || savingScope.current === version) return;
    savingScope.current = version;
    setSaving(true);
    let structureAdopted = false;
    try {
      await adoptPaperStructure(job.id, {
        expected_generation: preview.generation,
        preview_hash: preview.preview_hash,
        complete_paper_confirmed: complete,
      });
      structureAdopted = true;
      if (!isCurrent()) return;
      // 结构采用不会登记试卷文件；沿用真实来源登记，才能通过资料步骤的试卷文件检查。
      await registerConfirmedPaperMaterials(job, papers, () => isCurrent() && !unavailable.current);
      if (!isCurrent()) return;
      setOpen(false);
      await onAdopted();
      if (isCurrent()) message.success("考试资料已确认，请继续核对小题分值和评分配置");
    } catch (err) {
      await reportAdoptionFailure(err, structureAdopted);
    } finally {
      if (savingScope.current === version) savingScope.current = null;
      if (isCurrent()) setSaving(false);
    }
  }
  async function reportAdoptionFailure(error: unknown, structureAdopted: boolean) {
    if (!isCurrent()) return;
    if (!structureAdopted) {
      message.error(getUserErrorMessage(error, "调整失败，请重新打开预览核对"));
      return;
    }
    // 结构写入已成功；登记或回读失败仍读取已采用事实，后续补登记不能重写正式题目。
    setOpen(false);
    try { await onAdopted(); } catch { /* 回读失败保留原提示，允许重新读取或补登记。 */ }
    if (isCurrent()) message.error(`题目结构已采用，资料登记或状态刷新未完成：${getUserErrorMessage(error, "确认失败")}；请重试确认考试资料`);
  }
  if (job.structure_confirmed_generation === job.generation) return null;
  return (
    <>
      <Alert
        showIcon
        type={preview?.blocked_reasons.length ? "warning" : "info"}
        message="确认考试资料"
        description={
          error ||
          (!preview ? (
            "正在核对识别结构…"
          ) : (
            <>
              <p>
                现有配置 {preview.current_count} 题，资料识别 {preview.recognized_count} 题。
                {preview.missing_scores.length
                  ? `${preview.missing_scores.length} 题分值待确认`
                  : `资料分值合计 ${preview.recognized_total} 分`}
                ，考试预期满分 {preview.expected_total} 分。
              </p>
              <p>核对完整试卷及题目范围后确认，系统会登记试卷来源并采用题目结构。小题分值和评分配置在下一步核对。</p>
              {preview.blocked_reasons.map((reason) => (
                <p key={reason}>{reason}</p>
              ))}
            </>
          ))
        }
        action={
          <Button type="primary" disabled={disabled || saving || (!preview && !error)} onClick={() => void refreshAndOpen()}>
            {error ? "重试结构预览" : "确认考试资料"}
          </Button>
        }
      />
      <Modal
        title="确认考试资料与题目结构"
        open={open}
        width={900}
        onCancel={() => {
          if (!saving) setOpen(false);
        }}
        confirmLoading={saving}
        okText="确认资料并采用题目结构"
        onOk={() => void adopt()}
        okButtonProps={{ disabled: disabled || !complete || !!preview?.blocked_reasons.length }}
        destroyOnHidden
      >
        <Space direction="vertical" style={{ width: "100%" }}>
          <Alert
            showIcon
            type="info"
            message="请先确认上传了完整试卷"
            description="仅上传部分页面、答案或解析时，不能据此减少题目。未识别到可靠分值的小题将显示待确认，不能直接开始阅卷。已有答案和解析会保留。"
          />
          {preview?.blocked_reasons.length ? <Alert type="error" message={preview.blocked_reasons.join("；")} /> : null}
          <Table
            rowKey={(row) => `${row.question_id || row.question_no}:${row.action}`}
            size="small"
            pagination={false}
            scroll={{ y: 360 }}
            dataSource={preview?.changes ?? []}
            columns={[
              { title: "题号", dataIndex: "question_no", width: 65 },
              { title: "处理", dataIndex: "action", render: (value) => actions[value] },
              { title: "原题型", dataIndex: "previous_type", render: typeName },
              { title: "资料题型", dataIndex: "question_type", render: typeName },
              { title: "原分值", dataIndex: "previous_score", render: (value) => value ?? "—" },
              { title: "资料分值", dataIndex: "score", render: (value) => value ?? "待确认" },
              { title: "说明", dataIndex: "reason" },
            ]}
          />
          <Checkbox checked={complete} disabled={saving} onChange={(event) => setComplete(event.target.checked)}>
            我已核对资料完整，以上题目结构与实际试卷一致
          </Checkbox>
        </Space>
      </Modal>
    </>
  );
}
