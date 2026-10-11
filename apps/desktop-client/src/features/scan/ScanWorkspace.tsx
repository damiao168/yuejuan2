import { useState } from "react";
import { Alert, Button, Checkbox, Empty, Form, Input, InputNumber, Modal, Select, Space, Tag, Tooltip } from "antd";
import { CloudUpload, FileUp, ListChecks, RefreshCw, RotateCcw, Stethoscope, UploadCloud } from "lucide-react";
import { getSafeUserText } from "../../api/userError";
import type { SubmissionImageQuality } from "../../api/submissions";
import { ScanQueueRecovery, ScanQueueTable, SectionHead } from "../../components/DesktopStatusViews";
import type { CaptureScanContext } from "../../api/exams";
import { isTauriRuntime } from "../../lib/localRuntime";
import type { ScannerPreflightCheck } from "../../lib/scannerProfile";
import { genericStatusLabel, subjectLabels } from "../../statusLabels";
import type { SubmissionQualityResult } from "../../types";
import type { useExamCaptureContext } from "../capture/useExamCaptureContext";
import type { useSubmissionQuality } from "../quality/useSubmissionQuality";
import type { useScanQueue } from "../scan-queue/useScanQueue";
import type { useScannerController } from "../scanner/useScannerController";
import type { useUploadSync } from "../upload/useUploadSync";

type ScanWorkspaceProps = Readonly<{
  token: string | null;
  isOnline: boolean;
  capture: ReturnType<typeof useExamCaptureContext>;
  scanner: ReturnType<typeof useScannerController>;
  scanQueue: ReturnType<typeof useScanQueue>;
  uploadSync: ReturnType<typeof useUploadSync>;
  quality: ReturnType<typeof useSubmissionQuality>;
}>;

export function ScanWorkspace({ token, isOnline, capture, scanner, scanQueue, uploadSync, quality }: ScanWorkspaceProps) {
  const {
    exams, selectedExamId, setSelectedExamId, examError, isLoadingExams,
    scanSubmissionId, setScanSubmissionId, captureBatchId, setCaptureBatchId,
    captureBatches, setCaptureBatches, isLoadingCaptureBatches, scanStartPage,
    setScanStartPage, handleLoadExams, handleLoadCaptureBatches
  } = capture;
  const { expectedDpi } = scanner;
  const { queue, fileInputRef, handleFileSelection, clearSucceededQueueItems, fileImportError } = scanQueue;
  const { uploadQueueItem, uploadQueueItems } = uploadSync;
  const { qualityResult, qualityError, isCheckingQuality, handleRunQualityCheck } = quality;
  const selectedExam = exams.find((exam) => exam.id === selectedExamId);
  const selectedBatch = captureBatches.find((batch) => batch.id === captureBatchId && batch.exam_id === selectedExamId);
  const batchAcceptsUpload = Boolean(selectedBatch && !["completed", "cancelled"].includes(selectedBatch.status));
  const linkedSubmissions = [...new Set((capture.processingDetail?.pages ?? []).map((page) => page.submission_id).filter((id): id is string => Boolean(id)))];
  const [showAllQueue, setShowAllQueue] = useState(false);
  const allScanItems = queue.filter((item) => item.kind === "scan_upload");
  const scanItems = showAllQueue ? allScanItems : allScanItems.filter((item) => item.examId === selectedExamId && item.captureBatchId === captureBatchId);
  const currentItems = allScanItems.filter((item) => item.examId === selectedExamId && item.captureBatchId === captureBatchId);
  const readyCount = currentItems.filter((item) => item.status === "pending").length;
  const failedCount = currentItems.filter((item) => item.status === "failed").length;
  const [showTemplatePreview, setShowTemplatePreview] = useState(false);
  // 只有当前考试和批次的 ready 模板可用于新导入；切换后尚未更新的旧上下文不能放行。
  const template = capture.scanContext?.template_status === "ready" && capture.scanContext.batch.id === captureBatchId && capture.scanContext.exam.id === selectedExamId ? capture.scanContext.template : undefined;
  const importReady = Boolean(selectedExam && captureBatches.some((batch) => batch.id === captureBatchId.trim() && batch.exam_id === selectedExamId && !["completed", "cancelled"].includes(batch.status))
    && template && scanQueue.queueRestoreStatus === "ready" && Number.isInteger(expectedDpi) && expectedDpi >= 150 && expectedDpi <= 1200);

  return <div className="workspace-grid scan-workstation">
    <section className="panel full">
      <SectionHead icon={<UploadCloud size={20} />} title="扫描工作站" description="导入已有 PDF/图片无需连接本机扫描仪；确认考试、批次和答题卡模板参数后写入本机加密队列。" action={<Tag color={isOnline ? "success" : "error"}>{isOnline ? "在线" : "离线，上传暂停"}</Tag>} />
      <ScanTemplateControls token={token} isOnline={isOnline} capture={capture} scanner={scanner} template={template} showTemplatePreview={showTemplatePreview} onTemplatePreviewChange={setShowTemplatePreview} />
      <ScannerDeviceControls scanner={scanner} template={template} />
      <div className="scan-toolbar">
        <Form layout="vertical" className="scan-context-form">
          <Form.Item label="考试"><Space.Compact block><Select value={selectedExamId || undefined} placeholder={token ? "选择真实考试" : "登录后加载考试"} loading={isLoadingExams} disabled={!token} onChange={(examID) => { setSelectedExamId(examID); setCaptureBatchId(""); setCaptureBatches([]); }} options={exams.map((exam) => ({ value: exam.id, label: `${exam.name} / ${subjectLabels[exam.subject] ?? "其他学科"} / ${genericStatusLabel(exam.status)}` }))} /><Button icon={<RefreshCw size={16} />} disabled={!token} loading={isLoadingExams} onClick={handleLoadExams}>刷新</Button></Space.Compact></Form.Item>
          <Form.Item label="采集批次"><Space.Compact block><Select value={captureBatchId || undefined} placeholder={selectedExamId ? "选择批次上传或查看结果" : "请先选择考试"} loading={isLoadingCaptureBatches} disabled={!selectedExamId} onChange={setCaptureBatchId} options={captureBatches.map((batch) => ({ value: batch.id, label: `${batch.name} · ${genericStatusLabel(batch.status)}${["completed", "cancelled"].includes(batch.status) ? "（仅查看）" : ""} · ${batch.file_count} 个文件` }))} /><Button icon={<RefreshCw size={16} />} disabled={!selectedExamId} loading={isLoadingCaptureBatches} onClick={() => void handleLoadCaptureBatches()}>刷新</Button></Space.Compact></Form.Item>
          <Form.Item label="起始页码"><InputNumber min={1} value={scanStartPage} onChange={(value) => setScanStartPage(value ?? 1)} /></Form.Item>
        </Form>
        <div className="scan-actions">
          <input ref={fileInputRef} type="file" multiple accept=".pdf,.png,.jpg,.jpeg,.tif,.tiff" onChange={(event) => { void handleFileSelection(event.target.files); event.target.value = ""; }} />
          <Button icon={<FileUp size={16} />} disabled={!importReady} onClick={() => fileInputRef.current?.click()}>批量选择 PDF/图片（含 TIFF）</Button>
          <Button type="primary" icon={<UploadCloud size={16} />} disabled={!token || !isOnline || !batchAcceptsUpload || readyCount === 0} onClick={() => void uploadQueueItems("pending", { examId: selectedExamId, captureBatchId })}>上传当前批次 {readyCount} 个等待项</Button>
          <Button icon={<RotateCcw size={16} />} disabled={!token || !isOnline || !batchAcceptsUpload || failedCount === 0} onClick={() => void uploadQueueItems("failed", { examId: selectedExamId, captureBatchId })}>重试当前批次 {failedCount} 个失败项</Button>
          <Button danger disabled={!scanItems.some((item) => item.status === "succeeded")} onClick={() => void clearSucceededQueueItems()}>归档已确认项</Button>
        </div>
      </div>
      {examError && <Alert className="section-alert" type="error" message={examError} showIcon />}
      <ScanQueueRecovery status={scanQueue.queueRestoreStatus} error={scanQueue.queueRestoreError} onRetry={() => void scanQueue.restoreQueue()} />
      {fileImportError && <Alert className="section-alert" type="error" message={fileImportError} showIcon />}
      {scanQueue.importResults.length > 0 && <Alert className="section-alert" type={scanQueue.importResults.some((item) => !item.saved) ? "warning" : "success"} showIcon message={`本次导入：已保留 ${scanQueue.importResults.filter((item) => item.saved).length} 个，未保留 ${scanQueue.importResults.filter((item) => !item.saved).length} 个`}
        description={<div>{scanQueue.importResults.map((item, index) => <p key={`${item.filename}:${index}`}>{item.filename}：{item.detail}</p>)}</div>} />}
      {!token && <Alert className="section-alert" type="warning" message="未登录：不会读取考试，也不会上传文件。" showIcon />}
      {(!selectedExamId || !captureBatchId.trim()) && token && <p className="muted">请先选择真实考试和可继续上传的采集批次；客户端不会创建假考试上下文。</p>}
      {selectedExam && <p className="muted">当前考试：{selectedExam.name} / {subjectLabels[selectedExam.subject] ?? "其他学科"} / {genericStatusLabel(selectedExam.status)}</p>}
      {selectedBatch && !batchAcceptsUpload && <Alert type="info" showIcon message="当前批次仅供查看处理结果" description="该批次已结束，不能继续导入或上传。需要补采时，请学校管理员核对后重新开启或创建采集批次。" />}
    </section>
    <section className="panel full"><SectionHead icon={<ListChecks size={20} />} title="上传前预览与本地质量检查" description="默认只显示当前考试/批次。文件上传后仍需等待服务端匹配与质量检查。" action={<Checkbox checked={showAllQueue} onChange={(event) => setShowAllQueue(event.target.checked)}>查看本账号全部批次</Checkbox>} />
      {scanQueue.queueContextError && <Alert type="warning" showIcon message={scanQueue.queueContextError} />}
      <Button disabled={!token || !isOnline || !allScanItems.length} loading={scanQueue.loadingQueueContext} onClick={() => void scanQueue.refreshQueueContext()}>重新读取考试与批次名称</Button>
      {showAllQueue && <Button disabled={!token || !isOnline || !allScanItems.some((item) => item.status === "pending")} onClick={() => Modal.confirm({ title: "上传本账号全部批次的等待项？", content: "请核对表格中每份文件的考试和采集批次，再确认上传。", okText: "确认全部等待项", cancelText: "继续核对", onOk: () => uploadQueueItems("pending") })}>上传全部批次的等待项</Button>}
      <ScanQueueTable items={scanItems} emptyText={scanQueueEmptyText(scanQueue.queueRestoreStatus)} canRetry={(item) => item.examId !== selectedExamId || item.captureBatchId !== captureBatchId || batchAcceptsUpload} onRetry={(id) => void uploadQueueItem(id)} onRemove={(id) => void scanQueue.removeQueueItem(id)} onReinspect={(id) => void scanQueue.reinspectQueueItem(id)} onReplace={(id) => scanQueue.chooseReplacement(id)} />
    </section>
    <section className="panel full">
      <details><summary>高级关联与诊断（学校技术人员提供）</summary><Form.Item label="答卷标识（旧流程可选）"><Input value={scanSubmissionId} onChange={(event) => setScanSubmissionId(event.target.value)} placeholder="仅在学校技术人员提供答卷标识时手填" /></Form.Item></details>
      <SectionHead icon={<CloudUpload size={20} />} title="服务端处理状态" description="已确认上传表示服务端收到文件；页数完整性与图像质量分别显示，图像通过后才能继续识别与阅卷。" action={<Button icon={<RefreshCw size={16} />} loading={isCheckingQuality} disabled={!token || !isOnline || !batchAcceptsUpload || !quality.canRunQualityCheck || isCheckingQuality} onClick={handleRunQualityCheck}>检查页数完整性</Button>} />
      <Space wrap><Button disabled={!token || !captureBatchId} loading={capture.loadingProcessing} onClick={() => void capture.handleRefreshProcessing()}>刷新当前批次处理结果</Button>
        <Select value={linkedSubmissions.includes(scanSubmissionId) ? scanSubmissionId : undefined} placeholder="选择服务端已关联的答卷" disabled={!linkedSubmissions.length} onChange={setScanSubmissionId} options={linkedSubmissions.map((id, index) => ({ value: id, label: `答卷 ${index + 1} · ${capture.processingDetail!.pages.filter((page) => page.submission_id === id).length} 页` }))} /></Space>
      {capture.processingError && <Alert type="error" message={capture.processingError} showIcon />}
      {capture.processingDetail ? <div><p>当前批次：{capture.processingDetail.batch.name} / {genericStatusLabel(capture.processingDetail.batch.status)} / 已形成 {linkedSubmissions.length} 份答卷</p>
        {!linkedSubmissions.length && <Alert type="info" message="服务端尚未完成答卷匹配" description="请在学校管理端完成批次处理，或稍后刷新本页。收到文件不等于已形成答卷或质量通过。" showIcon />}
        {capture.processingDetail.files.map((file) => <p key={file.id}>{file.original_name}：{genericStatusLabel(file.status)}{file.error_code ? ` / 处理异常 ${getSafeUserText(file.error_code, "需学校管理员核对")}，请在批次处理页面修复后刷新` : ""}</p>)}</div> : <p className="muted">上传后点击刷新，服务端形成答卷后可直接选择运行质量检查。采集批次需先由学校管理端创建。</p>}
      {qualityError && <Alert type="error" message={qualityError} showIcon />}
      <SubmissionQualitySummary qualityResult={qualityResult} />
      <h4>服务端图像质量</h4>
      <Button disabled={!token || !isOnline || !quality.canRunQualityCheck} loading={quality.isReadingImageQuality} onClick={() => void quality.handleRefreshImageQuality()}>刷新图像检查状态</Button>
      {quality.imageQualityError && <Alert type="warning" showIcon message={quality.imageQualityError} />}
      <ImageQualitySummary value={quality.imageQuality} loading={quality.isReadingImageQuality} />
    </section>
  </div>;
}

function ScanTemplateControls({ token, isOnline, capture, scanner, template, showTemplatePreview, onTemplatePreviewChange }: Readonly<Pick<ScanWorkspaceProps, "token" | "isOnline" | "capture" | "scanner"> & {
  template: CaptureScanContext["template"] | undefined;
  showTemplatePreview: boolean;
  onTemplatePreviewChange: (open: boolean) => void;
}>) {
  const { selectedExamId, captureBatchId } = capture;
  const { expectedPaperSize, setExpectedPaperSize, expectedDpi, setExpectedDpi, setScannerPreflight } = scanner;
  return <>
    <Form layout="vertical" className="scan-context-form">
      <Form.Item label="本批答题卡模板"><div>{template ? <Space wrap><strong>{template.name} · 第 {template.version_no} 版 · {template.page_count} 页</strong><Button onClick={() => onTemplatePreviewChange(true)}>查看模板布局</Button></Space> : <span>{capture.loadingScanContext ? "正在读取当前批次模板…" : "模板尚未确认"}</span>}
        {!selectedExamId || !captureBatchId ? <p>请先在下方选择考试和采集批次。</p> : null}
        <Button disabled={!token || !isOnline || !captureBatchId} loading={capture.loadingScanContext} onClick={() => void capture.handleRefreshScanContext()}>重新读取模板</Button></div></Form.Item>
      <Form.Item label="导入检查纸张"><Select value={expectedPaperSize} onChange={(value) => { setExpectedPaperSize(value); setScannerPreflight(null); }} options={["A3", "A4", "A5", "Letter", "Legal"].map((value) => ({ value, label: value }))} /></Form.Item>
      <Form.Item label="导入检查清晰度（DPI）"><InputNumber min={150} max={1200} value={expectedDpi} onChange={(value) => { setExpectedDpi(value ?? 300); setScannerPreflight(null); }} /></Form.Item>
    </Form>
    <p className="muted">模板从当前采集批次所属考试自动读取，无需填写内部编号。模板未记录纸张尺寸和扫描清晰度，请按实际原件与扫描设置确认以上参数；本机检查不代表原件已匹配模板，上传后仍须通过服务端模板和质量门禁。</p>
    {capture.scanContextError && <Alert type="error" showIcon message={capture.scanContextError} description="当前模板未核实，暂停新导入。请点击重新读取模板；持续失败时联系学校管理员。" />}
    {capture.scanContext && capture.scanContext.template_status !== "ready" && <Alert type="warning" showIcon message={capture.scanContext.template_status === "missing" ? "本次考试尚未绑定答题卡模板" : "本次考试的模板绑定需要核对"} description="请学校管理员在本次考试的答题卡模板页面锁定并绑定模板；完成后在本页重新读取，扫描员无需进入考试配置。" />}
    <Modal title={template ? `${template.name} · 第 ${template.version_no} 版布局` : "答题卡模板布局"} open={showTemplatePreview && Boolean(template)} onCancel={() => onTemplatePreviewChange(false)} footer={<Button onClick={() => onTemplatePreviewChange(false)}>核对完成</Button>}>
      {template && <TemplateLayoutPreview template={template} />}
    </Modal>
  </>;
}

function ScannerDeviceControls({ scanner, template }: Readonly<Pick<ScanWorkspaceProps, "scanner"> & { template: CaptureScanContext["template"] | undefined }>) {
  const {
    scannerProfiles, selectedScannerProfileId, setSelectedScannerProfileId,
    scannerPreflight, setScannerPreflight, scannerIntegration, scannerPreflightError,
    isCheckingScannerPreflight, expectedPaperSize, setExpectedPaperSize,
    expectedDuplex,
    setExpectedDuplex, expectedDpi, setExpectedDpi, isScannerProfileModalOpen,
    setIsScannerProfileModalOpen, scannerDevices, isLoadingScannerDevices,
    isSavingScannerProfile, scannerProfileName, setScannerProfileName,
    scannerDeviceFingerprint, setScannerDeviceFingerprint, handleLoadScannerProfiles,
    handleOpenScannerProfileSetup, handleSaveScannerProfile, handleScannerPreflight
  } = scanner;
  return <>
    {isTauriRuntime() ? <div className="scanner-preflight">
      <Alert type={scannerPreflight?.readyToScan ? "success" : "info"} showIcon message={scannerPreflight?.readyToScan ? "设备扫描前检查已通过" : "设备采集检查（可选）"} description={scannerIntegration?.detail ?? "WIA 用于发现 Windows 扫描设备，当前直接采集尚未启用。已有文件导入无需设备检查，仍按下方模板参数进行样张检查并由服务端核对真实模板。"} />
      <Form layout="vertical" className="scan-context-form">
        <Form.Item label="扫描设备档案（设备采集用）"><Select value={selectedScannerProfileId || undefined} placeholder="选择已保存的扫描设备档案" onChange={(value) => {
          const profile = scannerProfiles.find((item) => item.id === value);
          setSelectedScannerProfileId(value);
          if (profile) { setExpectedPaperSize(profile.paperSize); setExpectedDuplex(profile.duplex); setExpectedDpi(profile.dpi); }
          setScannerPreflight(null);
        }} options={scannerProfiles.map((profile) => ({ value: profile.id, label: `${profile.name} · ${profile.dpi} DPI · ${profile.paperSize}` }))} /></Form.Item>
        <Form.Item label="模板双面"><Checkbox checked={expectedDuplex} onChange={(event) => { setExpectedDuplex(event.target.checked); setScannerPreflight(null); }}>双面扫描</Checkbox></Form.Item>
      </Form>
      <Space wrap><Button icon={<Stethoscope size={16} />} loading={isCheckingScannerPreflight} onClick={() => void handleScannerPreflight()}>运行设备采集检查</Button><Button loading={isLoadingScannerDevices} onClick={() => void handleOpenScannerProfileSetup()}>新建扫描档案</Button><Button icon={<RefreshCw size={16} />} onClick={() => void handleLoadScannerProfiles()}>刷新设备档案</Button></Space>
      {scannerPreflightError && <Alert className="section-alert" type="error" showIcon message={scannerPreflightError} />}
      {scannerPreflight && <div className="quality-checks">{scannerPreflight.checks.map((check) => <Tooltip key={check.key} title={check.detail}><Tag color={scannerPreflightTone(check.status)}>{check.label}</Tag></Tooltip>)}</div>}
    </div> : <Alert type="info" showIcon message="浏览器开发模式不提供扫描设备能力；请使用 Windows 桌面扫描站完成现场采集。" />}
    <Modal title="新建扫描档案" open={isScannerProfileModalOpen} confirmLoading={isSavingScannerProfile} okText="保存并使用" cancelText="取消" onCancel={() => setIsScannerProfileModalOpen(false)} onOk={() => void handleSaveScannerProfile()}>
      <p className="muted">档案只保存本机设备指纹与采集参数；不上传设备序列号，也不代表已通过真实设备验收。</p>
      <Form layout="vertical">
        <Form.Item label="档案名称" required><Input value={scannerProfileName} onChange={(event) => setScannerProfileName(event.target.value)} placeholder="例如：教务处扫描站 A4 双面" /></Form.Item>
        <Form.Item label="Windows 已发现的扫描设备" required><Select value={scannerDeviceFingerprint || undefined} placeholder="选择设备" options={scannerDevices.map((device) => ({ value: device.fingerprint, label: `${device.displayName} / ${device.driverStatus}` }))} onChange={setScannerDeviceFingerprint} notFoundContent="未发现可用设备；请确认驱动、连接和 Windows WIA 服务。" /></Form.Item>
        <Form.Item label="当前考试答题卡模板"><span>{template ? `${template.name} · 第 ${template.version_no} 版（自动读取）` : "请先选择批次并读取已锁定模板"}</span></Form.Item>
        <Space wrap><Form.Item label="纸张"><Select value={expectedPaperSize} onChange={setExpectedPaperSize} options={["A3", "A4", "A5", "Letter", "Legal"].map((value) => ({ value, label: value }))} /></Form.Item><Form.Item label="DPI"><InputNumber min={150} max={1200} value={expectedDpi} onChange={(value) => setExpectedDpi(value ?? 300)} /></Form.Item><Form.Item label="双面"><Checkbox checked={expectedDuplex} onChange={(event) => setExpectedDuplex(event.target.checked)}>双面扫描</Checkbox></Form.Item></Space>
      </Form>
    </Modal>
  </>;
}

function scanQueueEmptyText(status: ScanWorkspaceProps["scanQueue"]["queueRestoreStatus"]) {
  // 队列尚未读取成功时不能显示为空队列；ready 使用表格原有的默认空态。
  if (status === "ready") return undefined;
  if (status === "loading") return "正在读取本机队列";
  return "原队列尚未核实，请先重新读取";
}

function TemplateLayoutPreview({ template }: Readonly<{ template: NonNullable<CaptureScanContext["template"]> }>) {
  return <div><p>以下为已锁定版本的真实区域布局，供核对页数、身份栏和答题区；不含打印原图，不能作为打印原件。</p>
    {template.layout.pages.map((page) => <div key={page.page_no}><h4>第 {page.page_no} 页</h4><svg role="img" aria-label={`第 ${page.page_no} 页模板区域布局`} viewBox={`0 0 ${page.width} ${page.height}`} style={{ width: "100%", maxHeight: 450, border: "1px solid #d9d9d9" }}>
      <rect width={page.width} height={page.height} fill="white" />
      {[...page.registration_marks.map((region) => ({ region, color: "#444", kind: "定位标记" })), ...page.identity_regions.map((region) => ({ region, color: "#fa8c16", kind: "身份栏" })), ...page.question_regions.map((region) => ({ region, color: "#1677ff", kind: "答题区" }))].map(({ region, color, kind }, index) => <g key={`${kind}:${region.id}:${index}`}><rect x={region.x} y={region.y} width={region.width} height={region.height} stroke={color} fill={color} fillOpacity={0.08} strokeWidth={Math.max(1, page.width / 500)} /><title>{region.label || kind}</title></g>)}
    </svg><p>灰色：定位标记 · 橙色：身份栏 · 蓝色：答题区</p></div>)}</div>;
}

function scannerPreflightTone(status: ScannerPreflightCheck["status"]) {
  if (status === "passed") return "success";
  if (status === "failed") return "error";
  return "warning";
}

function SubmissionQualitySummary({ qualityResult }: Readonly<{ qualityResult: SubmissionQualityResult | null }>) {
  if (!qualityResult) return <Empty description="尚未检查页数完整性" />;
  return <div className="quality-result"><Tag color={qualityResult.valid ? "success" : "warning"}>{qualityResult.valid ? "页数完整" : "页数需核对"}</Tag>{qualityResult.issues.length ? qualityResult.issues.map((issue) => <p key={`${issue.code}-${issue.message}`}>{getSafeUserText(issue.message, "页数完整性未通过，请核对缺页和页码")}</p>) : <p>页数与页码检查通过。图像质量请以下方服务端结论为准。</p>}</div>;
}

function ImageQualitySummary({ value, loading }: Readonly<{ value: SubmissionImageQuality | null; loading: boolean }>) {
  if (!value) return <p className="muted">{loading ? "正在读取所选答卷的图像检查结果…" : "图像检查结果尚未确认。请选择本批次已关联的答卷，再刷新查看；页数完整不代表图像通过。"}</p>;
  const states: Record<string, { label: string; next: string; color: "success" | "warning" | "error" | "info" }> = {
    passed: { label: "图像质量已通过", next: "请在学校管理端继续文字识别与阅卷，具体进度以考试处理页面为准。", color: "success" },
    review: { label: "图像质量待人工核对", next: "请学校管理员在本次考试的答卷处理页面核对问题图像；核对完成后刷新本页。", color: "warning" },
    failed: { label: "图像质量未通过", next: "请学校管理员在本次考试的答卷处理页面修复或重新上传问题图像，重新检查后刷新本页。", color: "error" },
    unchecked: { label: "图像质量尚未通过检查", next: "请等待服务端图像检查；若一直未开始，请学校管理员在本批次处理页面启动检查，再刷新本页。", color: "info" }
  };
  const state = states[value.quality_status] ?? { label: "图像检查结果尚未确认", next: "请稍后刷新；若持续未确认，请学校管理员核对本批次处理情况。", color: "info" as const };
  return <Alert className="section-alert" type={state.color} showIcon message={state.label}
    description={<div><p>{state.next}</p>{(value.quality_issues ?? []).map((issue, index) => <p key={`${issue.code}:${index}`}>{getSafeUserText(issue.message, "存在待核对图像，请管理员查看本批次详情")}</p>)}</div>} />;
}
