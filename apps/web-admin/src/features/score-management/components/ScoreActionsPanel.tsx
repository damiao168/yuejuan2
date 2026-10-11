import { Alert, Button, Divider, Input } from "antd";
import { Calculator, CheckCircle2, Download, FileWarning, LockKeyhole, Send } from "lucide-react";
import type { AuditLog } from "../../../api/audit";
import type { QualityCheckResult } from "../../../api/scores";
import { AuditPanel } from "./AuditPanel";

type ScoreActionsPanelProps = Readonly<{
  publishedOrLocked: boolean;
  canWrite: boolean;
  selectedExamId: string;
  gradeTotal: number;
  quality: QualityCheckResult | null;
  actioning: string | null;
  confirmReason: string;
  publishReason: string;
  setConfirmReason: (reason: string) => void;
  setPublishReason: (reason: string) => void;
  finalize: () => Promise<void>;
  confirmGrades: () => Promise<void>;
  publish: () => void;
  exportGrades: () => void;
  lastWatermark: string;
  canReadAudit: boolean;
  scoreAuditLogs: AuditLog[];
}>;

export function ScoreActionsPanel(props: ScoreActionsPanelProps) {
  const {
    publishedOrLocked, canWrite, selectedExamId, gradeTotal, quality, actioning,
    confirmReason, publishReason, setConfirmReason, setPublishReason, finalize,
    confirmGrades, publish, exportGrades, lastWatermark, canReadAudit, scoreAuditLogs
  } = props;
  // 汇总和确认供门户版本共同使用；基础公开是另一个入口的可选操作，不能暗示必须双发布。
  return <aside className="score-actions-panel">
    <div className="panel-head">
      <div><h2>成绩汇总与确认</h2><p>{publishedOrLocked ? "基础成绩已锁定" : "先汇总、确认，再到版本区域预览并发布到学生门户"}</p></div>
      {publishedOrLocked ? <LockKeyhole size={18} /> : <Calculator size={18} />}
    </div>
    {publishedOrLocked ? <Alert type="success" showIcon message="基础成绩已锁定" description="基础成绩不能直接修改；学生门户版本的状态请查看本页版本区域。更正请通过申诉或复评流程处理。" /> : null}
    <div className="score-step-group">
      <span className="score-step-title">第 1 步 · 汇总最终成绩</span>
      <Button block icon={<Calculator size={16} />} disabled={!canWrite || !selectedExamId || publishedOrLocked} loading={actioning === "finalize"} onClick={() => void finalize()}>汇总最终成绩</Button>
    </div>
    <div className="score-step-group">
      <span className="score-step-title">第 2 步 · 确认成绩</span>
      <label className="score-step-label" htmlFor="score-confirm-reason">确认原因（必填）</label>
      <Input.TextArea id="score-confirm-reason" rows={3} value={confirmReason} placeholder="例如：已由学科组长复核，成绩无误" onChange={(event) => setConfirmReason(event.target.value)} />
      <Button block icon={<CheckCircle2 size={16} />} disabled={!canWrite || gradeTotal === 0 || publishedOrLocked} loading={actioning === "confirm"} onClick={() => void confirmGrades()}>确认成绩</Button>
    </div>
    <details className="score-advanced-details">
      <summary>学校原有成绩查询入口（独立公开）</summary>
      <p>仅在学校使用原有成绩查询入口时操作。此操作锁定基础成绩，不会发布学生门户版本。</p>
      <div className="score-step-group">
      <span className="score-step-title">公开学校原有查询入口</span>
      <label className="score-step-label" htmlFor="score-publish-reason">发布原因（必填）</label>
      <Input.TextArea id="score-publish-reason" rows={3} value={publishReason} placeholder="例如：经教务处审批，同意发布" onChange={(event) => setPublishReason(event.target.value)} />
      <Button block type="primary" icon={<Send size={16} />} disabled={!canWrite || !quality?.can_publish || publishedOrLocked} loading={actioning === "publish"} onClick={publish}>公开并锁定基础成绩</Button>
      </div>
    </details>
    <Divider className="score-step-divider" />
    <div className="score-step-group">
      <span className="score-step-title">导出</span>
      <Button block icon={<Download size={16} />} disabled={!canWrite || gradeTotal === 0} loading={actioning === "export"} onClick={exportGrades}>导出成绩</Button>
    </div>
    <details className="score-advanced-details">
      <summary>操作记录与导出水印</summary>
      <Alert type="info" showIcon icon={<FileWarning size={18} />} message="导出记录与水印" description={lastWatermark ? `最近一次导出的水印编号：${lastWatermark}` : "每次导出都会记录操作人和时间，导出文件自带可追溯水印。"} />
      <AuditPanel canRead={canReadAudit} logs={scoreAuditLogs} />
    </details>
  </aside>;
}
