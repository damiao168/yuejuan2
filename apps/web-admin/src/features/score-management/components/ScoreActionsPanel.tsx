import { Alert, Button, Divider } from "antd";
import { Calculator, CheckCircle2, Download, FileWarning, LockKeyhole, Send } from "lucide-react";
import type { AuditLog } from "../../../api/audit";
import type { QualityCheckResult } from "../../../api/scores";
import { AuditPanel } from "./AuditPanel";
import { BusinessReasonField } from "../../../components/BusinessReasonField";

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
  legacyPublicationLabel?: string;
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
      <div><h2>成绩汇总与确认</h2><p>{publishedOrLocked ? "基础成绩已锁定" : "汇总并确认基础成绩。"}</p></div>
      {publishedOrLocked ? <LockKeyhole size={18} /> : <Calculator size={18} />}
    </div>
    {publishedOrLocked ? <Alert type="success" showIcon message="基础成绩已锁定" description="更正须通过申诉或复评；学生门户状态见版本区域。" /> : null}
    <div className="score-step-group">
      <span className="score-step-title">第 1 步 · 汇总最终成绩</span>
      <Button block icon={<Calculator size={16} />} disabled={!canWrite || !selectedExamId || publishedOrLocked} loading={actioning === "finalize"} onClick={() => void finalize()}>汇总最终成绩</Button>
    </div>
    <div className="score-step-group">
      <span className="score-step-title">第 2 步 · 确认成绩</span>
      <BusinessReasonField id="score-confirm-reason" label="确认依据（必选）" value={confirmReason} onChange={setConfirmReason} choices={["已核对，本场成绩无误", "已完成学科组复核"]} />
      <Button block icon={<CheckCircle2 size={16} />} disabled={!canWrite || gradeTotal === 0 || publishedOrLocked} loading={actioning === "confirm"} onClick={() => void confirmGrades()}>确认成绩</Button>
    </div>
    <details className="score-advanced-details">
      <summary>学校原有成绩查询入口（独立公开）</summary>
      {props.legacyPublicationLabel ? <strong>{props.legacyPublicationLabel}</strong> : null}
      <p>仅用于学校原查询入口。公开后锁定基础成绩；学生门户需另行发布。</p>
      <div className="score-step-group">
      <span className="score-step-title">公开学校原有查询入口</span>
      <BusinessReasonField id="score-publish-reason" label="发布依据（必选）" value={publishReason} onChange={setPublishReason} choices={["已完成核对，同意发布本场成绩", "已经教务审批，同意发布"]} />
      <Button block type="primary" icon={<Send size={16} />} disabled={!canWrite || !quality?.can_publish || publishedOrLocked} loading={actioning === "publish"} onClick={publish}>公开并锁定基础成绩</Button>
      </div>
    </details>
    <Divider className="score-step-divider" />
    <div className="score-step-group">
      <span className="score-step-title">导出</span>
      <Button block icon={<Download size={16} />} disabled={!canWrite || gradeTotal === 0} loading={actioning === "export"} onClick={exportGrades}>导出本场全部成绩 CSV</Button>
    </div>
    <details className="score-advanced-details">
      <summary>操作记录与导出水印</summary>
      <Alert type="info" showIcon icon={<FileWarning size={18} />} message="导出记录与水印" description={lastWatermark ? `最近一次导出的水印编号：${lastWatermark}` : "每次导出都会记录操作人和时间，导出文件自带可追溯水印。"} />
      <AuditPanel canRead={canReadAudit} logs={scoreAuditLogs} />
    </details>
  </aside>;
}
