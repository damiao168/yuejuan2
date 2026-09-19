import { Alert, Button, Checkbox, Empty, Form, Input, InputNumber, Modal, Select, Space, Tag, Tooltip } from "antd";
import { CloudUpload, FileUp, ListChecks, RefreshCw, RotateCcw, Stethoscope, UploadCloud } from "lucide-react";
import { getSafeUserText } from "../../api/userError";
import { ScanQueueTable, SectionHead } from "../../components/DesktopStatusViews";
import { isTauriRuntime } from "../../lib/localRuntime";
import { genericStatusLabel, subjectLabels } from "../../statusLabels";
import type { useExamCaptureContext } from "../capture/useExamCaptureContext";
import type { useSubmissionQuality } from "../quality/useSubmissionQuality";
import type { useScanQueue } from "../scan-queue/useScanQueue";
import type { useScannerController } from "../scanner/useScannerController";
import type { useUploadSync } from "../upload/useUploadSync";

export function ScanWorkspace({ token, isOnline, capture, scanner, scanQueue, uploadSync, quality }: {
  token: string | null;
  isOnline: boolean;
  capture: ReturnType<typeof useExamCaptureContext>;
  scanner: ReturnType<typeof useScannerController>;
  scanQueue: ReturnType<typeof useScanQueue>;
  uploadSync: ReturnType<typeof useUploadSync>;
  quality: ReturnType<typeof useSubmissionQuality>;
}) {
  const {
    exams, selectedExamId, setSelectedExamId, examError, isLoadingExams,
    scanSubmissionId, setScanSubmissionId, captureBatchId, setCaptureBatchId,
    captureBatches, setCaptureBatches, isLoadingCaptureBatches, scanStartPage,
    setScanStartPage, handleLoadExams, handleLoadCaptureBatches
  } = capture;
  const {
    scannerProfiles, selectedScannerProfileId, setSelectedScannerProfileId,
    scannerPreflight, setScannerPreflight, scannerIntegration, scannerPreflightError,
    isCheckingScannerPreflight, expectedPaperSize, setExpectedPaperSize,
    expectedTemplatePreset, setExpectedTemplatePreset, expectedDuplex,
    setExpectedDuplex, expectedDpi, setExpectedDpi, isScannerProfileModalOpen,
    setIsScannerProfileModalOpen, scannerDevices, isLoadingScannerDevices,
    isSavingScannerProfile, scannerProfileName, setScannerProfileName,
    scannerDeviceFingerprint, setScannerDeviceFingerprint, handleLoadScannerProfiles,
    handleOpenScannerProfileSetup, handleSaveScannerProfile, handleScannerPreflight
  } = scanner;
  const { queue, fileInputRef, handleFileSelection, clearSucceededQueueItems } = scanQueue;
  const { uploadQueueItem, uploadQueueItems } = uploadSync;
  const { qualityResult, qualityError, isCheckingQuality, handleRunQualityCheck } = quality;
  const selectedExam = exams.find((exam) => exam.id === selectedExamId);
  const scanItems = queue.filter((item) => item.kind === "scan_upload");
  const readyCount = scanItems.filter((item) => item.status === "pending").length;
  const failedCount = scanItems.filter((item) => item.status === "failed").length;
  const scannerReady = !isTauriRuntime() || scannerPreflight?.readyToScan === true;

  return <div className="workspace-grid scan-workstation">
    <section className="panel full">
      <SectionHead icon={<UploadCloud size={20} />} title="扫描工作站" description="先完成扫描设备预检，再把 PDF/图片写入本地加密队列；断网不影响继续采集。" action={<Tag color={isOnline ? "success" : "error"}>{isOnline ? "在线" : "离线，上传暂停"}</Tag>} />
      {isTauriRuntime() ? <div className="scanner-preflight">
        <Alert type={scannerPreflight?.readyToScan ? "success" : "info"} showIcon message={scannerPreflight?.readyToScan ? "扫描前检查已通过" : "请先确认扫描设备与锁定答题卡模板"} description={scannerIntegration?.detail ?? "WIA 仅用于发现 Windows 已安装的扫描设备；直接采集需完成现场设备验证。"} />
        <Form layout="vertical" className="scan-context-form">
          <Form.Item label="扫描设备 Profile"><Select value={selectedScannerProfileId || undefined} placeholder="选择已保存的扫描设备 Profile" onChange={(value) => {
            const profile = scannerProfiles.find((item) => item.id === value);
            setSelectedScannerProfileId(value);
            if (profile) { setExpectedPaperSize(profile.paperSize); setExpectedTemplatePreset(profile.templatePreset); setExpectedDuplex(profile.duplex); setExpectedDpi(profile.dpi); }
            setScannerPreflight(null);
          }} options={scannerProfiles.map((profile) => ({ value: profile.id, label: `${profile.name} · ${profile.dpi} DPI · ${profile.paperSize}` }))} /></Form.Item>
          <Form.Item label="锁定答题卡模板 Profile"><Input value={expectedTemplatePreset} onChange={(event) => { setExpectedTemplatePreset(event.target.value); setScannerPreflight(null); }} placeholder="填写本批答题卡模板的 Profile 标识" /></Form.Item>
          <Form.Item label="模板纸张"><Select value={expectedPaperSize} onChange={(value) => { setExpectedPaperSize(value); setScannerPreflight(null); }} options={["A3", "A4", "A5", "Letter", "Legal"].map((value) => ({ value, label: value }))} /></Form.Item>
          <Form.Item label="模板 DPI"><InputNumber min={150} max={1200} value={expectedDpi} onChange={(value) => { setExpectedDpi(value ?? 300); setScannerPreflight(null); }} /></Form.Item>
          <Form.Item label="模板双面"><Checkbox checked={expectedDuplex} onChange={(event) => { setExpectedDuplex(event.target.checked); setScannerPreflight(null); }}>双面扫描</Checkbox></Form.Item>
        </Form>
        <Space wrap><Button icon={<Stethoscope size={16} />} loading={isCheckingScannerPreflight} onClick={() => void handleScannerPreflight()}>运行扫描前检查</Button><Button loading={isLoadingScannerDevices} onClick={() => void handleOpenScannerProfileSetup()}>新建扫描档案</Button><Button icon={<RefreshCw size={16} />} onClick={() => void handleLoadScannerProfiles()}>刷新设备 Profile</Button></Space>
        {scannerPreflightError && <Alert className="section-alert" type="error" showIcon message={scannerPreflightError} />}
        {scannerPreflight && <div className="quality-checks">{scannerPreflight.checks.map((check) => <Tooltip key={check.key} title={check.detail}><Tag color={check.status === "passed" ? "success" : check.status === "failed" ? "error" : "warning"}>{check.label}</Tag></Tooltip>)}</div>}
      </div> : <Alert type="info" showIcon message="浏览器开发模式不提供扫描设备能力；请使用 Windows 桌面扫描站完成现场采集。" />}
      <Modal title="新建扫描档案" open={isScannerProfileModalOpen} confirmLoading={isSavingScannerProfile} okText="保存并使用" cancelText="取消" onCancel={() => setIsScannerProfileModalOpen(false)} onOk={() => void handleSaveScannerProfile()}>
        <p className="muted">档案只保存本机设备指纹与采集参数；不上传设备序列号，也不代表已通过真实设备验收。</p>
        <Form layout="vertical">
          <Form.Item label="档案名称" required><Input value={scannerProfileName} onChange={(event) => setScannerProfileName(event.target.value)} placeholder="例如：教务处扫描站 A4 双面" /></Form.Item>
          <Form.Item label="Windows 已发现的扫描设备" required><Select value={scannerDeviceFingerprint || undefined} placeholder="选择设备" options={scannerDevices.map((device) => ({ value: device.fingerprint, label: `${device.displayName} / ${device.driverStatus}` }))} onChange={setScannerDeviceFingerprint} notFoundContent="未发现可用设备；请确认驱动、连接和 Windows WIA 服务。" /></Form.Item>
          <Form.Item label="答题卡模板标识" required><Input value={expectedTemplatePreset} onChange={(event) => setExpectedTemplatePreset(event.target.value)} placeholder="与本次采集批次的模板一致" /></Form.Item>
          <Space wrap><Form.Item label="纸张"><Select value={expectedPaperSize} onChange={setExpectedPaperSize} options={["A3", "A4", "A5", "Letter", "Legal"].map((value) => ({ value, label: value }))} /></Form.Item><Form.Item label="DPI"><InputNumber min={150} max={1200} value={expectedDpi} onChange={(value) => setExpectedDpi(value ?? 300)} /></Form.Item><Form.Item label="双面"><Checkbox checked={expectedDuplex} onChange={(event) => setExpectedDuplex(event.target.checked)}>双面扫描</Checkbox></Form.Item></Space>
        </Form>
      </Modal>
      <div className="scan-toolbar">
        <Form layout="vertical" className="scan-context-form">
          <Form.Item label="考试"><Space.Compact block><Select value={selectedExamId || undefined} placeholder={token ? "选择真实考试" : "登录后加载考试"} loading={isLoadingExams} disabled={!token} onChange={(examID) => { setSelectedExamId(examID); setCaptureBatchId(""); setCaptureBatches([]); }} options={exams.map((exam) => ({ value: exam.id, label: `${exam.name} / ${subjectLabels[exam.subject] ?? "其他学科"} / ${genericStatusLabel(exam.status)}` }))} /><Button icon={<RefreshCw size={16} />} disabled={!token} loading={isLoadingExams} onClick={handleLoadExams}>刷新</Button></Space.Compact></Form.Item>
          <Form.Item label="Submission ID（可选，仅用于旧流程的人工关联）"><Input value={scanSubmissionId} onChange={(event) => setScanSubmissionId(event.target.value)} placeholder="无需逐份填写；服务端按采集批次处理" /></Form.Item>
          <Form.Item label="采集批次"><Space.Compact block><Select value={captureBatchId || undefined} placeholder={selectedExamId ? "选择可继续上传的采集批次" : "请先选择考试"} loading={isLoadingCaptureBatches} disabled={!selectedExamId} onChange={setCaptureBatchId} options={captureBatches.map((batch) => ({ value: batch.id, label: `${batch.name} · ${genericStatusLabel(batch.status)} · ${batch.file_count} 个文件` }))} /><Button icon={<RefreshCw size={16} />} disabled={!selectedExamId} loading={isLoadingCaptureBatches} onClick={() => void handleLoadCaptureBatches()}>刷新</Button></Space.Compact><Input className="scan-batch-manual-input" value={captureBatchId} onChange={(event) => setCaptureBatchId(event.target.value.trim())} placeholder="列表不可用时可手工输入 capture batch UUID（兼容旧流程）" aria-label="手工输入采集批次 UUID" /></Form.Item>
          <Form.Item label="起始页码"><InputNumber min={1} value={scanStartPage} onChange={(value) => setScanStartPage(value ?? 1)} /></Form.Item>
        </Form>
        <div className="scan-actions">
          <input ref={fileInputRef} type="file" multiple accept=".pdf,.png,.jpg,.jpeg,.tif,.tiff" onChange={(event) => { void handleFileSelection(event.target.files); event.target.value = ""; }} />
          <Button icon={<FileUp size={16} />} disabled={!selectedExamId || !captureBatchId.trim() || !scannerReady} onClick={() => fileInputRef.current?.click()}>批量选择 PDF/图片（含 TIFF）</Button>
          <Button type="primary" icon={<UploadCloud size={16} />} disabled={!token || !isOnline || readyCount === 0} onClick={() => void uploadQueueItems("pending")}>上传等待中的项目</Button>
          <Button icon={<RotateCcw size={16} />} disabled={!token || !isOnline || failedCount === 0} onClick={() => void uploadQueueItems("failed")}>重试失败项目</Button>
          <Button danger disabled={!scanItems.some((item) => item.status === "succeeded")} onClick={() => void clearSucceededQueueItems()}>归档已确认项</Button>
        </div>
      </div>
      {examError && <Alert className="section-alert" type="error" message={examError} showIcon />}
      {!token && <Alert className="section-alert" type="warning" message="未登录：不会读取考试，也不会上传文件。" showIcon />}
      {(!selectedExamId || !captureBatchId.trim()) && token && <p className="muted">请先选择真实考试和可继续上传的采集批次；客户端不会创建假考试上下文。</p>}
      {selectedExam && <p className="muted">当前考试：{selectedExam.name} / {subjectLabels[selectedExam.subject] ?? "其他学科"} / {genericStatusLabel(selectedExam.status)}</p>}
    </section>
    <section className="panel full"><SectionHead icon={<ListChecks size={20} />} title="上传前预览与本地质量检查" description="文件进入队列后先展示检查结果；PDF 页数与复杂分辨率检测明确预留。" /><ScanQueueTable items={scanItems} onRetry={(id) => void uploadQueueItem(id)} /></section>
    <section className="panel full">
      <SectionHead icon={<CloudUpload size={20} />} title="服务端处理状态" description="文件上传后显示 file_asset 和 submission page 关联结果；质量门禁调用真实 submission API。" action={<Button icon={<RefreshCw size={16} />} loading={isCheckingQuality} disabled={!token || !scanSubmissionId.trim()} onClick={handleRunQualityCheck}>运行服务端质量门禁</Button>} />
      {qualityError && <Alert type="error" message={qualityError} showIcon />}
      {qualityResult ? <div className="quality-result"><Tag color={qualityResult.valid ? "success" : "warning"}>{qualityResult.valid ? "valid" : "issues"}</Tag>{qualityResult.issues.length ? qualityResult.issues.map((issue) => <p key={`${issue.code}-${issue.message}`}>{getSafeUserText(issue.message, "扫描质量检查未通过")}</p>) : <p>服务端质量门禁未返回问题。</p>}</div> : <Empty description="尚未运行服务端质量门禁" />}
    </section>
  </div>;
}
