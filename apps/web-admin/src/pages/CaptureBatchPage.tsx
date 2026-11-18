import { Alert, Button, Empty, Form, Input, Modal, Select, Space } from "antd";
import { Plus, RefreshCw, } from "lucide-react";
import { ErrorState, LoadingState } from "../components/PageState";
import { RegistrationCorrectionWorkspace } from "../components/RegistrationCorrectionWorkspace";
import { CaptureCollectionPanel } from "../features/capture/CaptureCollectionPanel";
import { hashQueryParam } from "../router/query";
import { useCaptureBatchPage } from "./useCaptureBatchPage";

import { CaptureBatchListPanel, CaptureBatchWorkspace } from "../features/capture/CaptureBatchPanels";
import { CaptureFileOrderPreview, captureSelectionError } from "../features/capture/CaptureFileOrderPreview";
export function CaptureBatchPage(props: Parameters<typeof useCaptureBatchPage>[0]) {
  const {
    user, examId, onCollectionChanged,
    message, form, batches, selectedId,
    setSelectedId, loading, loadingMoreBatches, hasMoreBatches,
    error, loadBatches, loadMoreBatches, modalOpen,
    setModalOpen, creating, newFilesInput, newFiles,
    setNewFiles, newPdfPending, setNewPdfPending, appendPdfPending,
    setAppendPdfPending, appendPreview, setAppendPreview, pendingCreation,
    actioning, preview, setPreview, correctionRunId,
    setCorrectionRunId, activeTab, setActiveTab, pagesPerStudent,
    setPagesPerStudent, locatedSubmission, setLocatedSubmission, setLocatedBatch,
    locationError, setLocationAttempt, locating, setLocating,
    locationChoices, replacement, setReplacement, detail,
    detailError, detailLoading, matching, matchingError,
    matchingLoading, processingSummaries, loadDetail, loadMatching,
    uploading, pendingUpload, retryUpload, discardUpload,
    collection, supplement, canCreateBatch, reopenOpen,
    setReopenOpen, reopenReason, setReopenReason, qualityOverridePage,
    setQualityOverridePage, qualityOverrideReason, setQualityOverrideReason, previewRequestRef,
    canEditSelectedBatch, batchCanManage, matchingReadOnlyReason, submitBatch,
    uploadProps, confirmAppendPreview, startProcessing, reopenBatch,
    confirmReopen, startPageProcessing, confirmQualityOverride, showPage,
    matchStudent, markUnknown, matchPage, splitSubmission,
    mergeSubmissions, fileColumns, pageColumns, progressPercent,
    summary, submissions, issuePages, locateCollectionTarget,
    prepareReplacement, resolveRegistration,
  } = useCaptureBatchPage(props);

  const renderBatchListState = () => {
    if (loading) return <LoadingState label="正在加载采集批次" />;
    if (error) return <ErrorState message={`采集批次加载失败：${error}`} onRetry={() => void loadBatches()} />;
    if (batches.length === 0) return <Empty description="尚未创建采集批次"><Button type="primary" onClick={() => setModalOpen(true)} disabled={!canCreateBatch}>创建第一个批次</Button></Empty>;
    return undefined;
  };
  const openNewBatch = () => {
    // 上次创建结果待确认时恢复原操作，不能再选文件开启另一批次。
    if (pendingCreation) {
      setModalOpen(true);
      return;
    }
    form.setFieldsValue({ name: `答卷导入 ${new Date().toLocaleString("zh-CN", { hour12: false })}`, source_type: "web_upload" });
    newFilesInput.current?.click();
  };

  if (correctionRunId) return <RegistrationCorrectionWorkspace runId={correctionRunId} canManage={batchCanManage} showCoordinates onClose={() => setCorrectionRunId(undefined)} onChanged={async () => { await loadDetail(selectedId, true); }} />;
  const collectionPanel = <CaptureCollectionPanel key={`${user.tenant}:${user.id}:${examId}`} examId={examId} actorKey={`${user.tenant}:${user.id}`} summary={collection.summary} loading={collection.loading} error={collection.error}
    canManage={canCreateBatch} localBusy={uploading || Boolean(pendingUpload) || actioning || creating || locating}
    onRefresh={collection.load} onLocate={target => void locateCollectionTarget(target)}
    onReplace={prepareReplacement}
    onCompleted={async value => {
      collection.acceptSummary(value);
      await Promise.all([loadBatches(), loadDetail(selectedId, true), onCollectionChanged?.()]);
    }}
    onMaterialChanged={async batchId => { await loadBatches(); if (batchId === selectedId) await loadDetail(batchId, true); }} />;
  const renderNewBatchModal = () => {
    const importTitle = supplement ? "导入补传答卷" : "导入答卷";
    const importConfirmText = newFiles.length > 0 ? "确认导入并处理" : "创建";
    const batchTitle = pendingCreation ? "恢复上次采集批次创建" : importTitle;
    const batchConfirmText = pendingCreation ? "继续确认原操作" : importConfirmText;
    return <Modal
        title={batchTitle}
        okText={batchConfirmText}
        open={modalOpen}
        closable={!creating} maskClosable={!creating}
        onCancel={() => { if (!creating) { setModalOpen(false); setNewFiles([]); } }}
        onOk={() => void submitBatch()}
        confirmLoading={creating}
        okButtonProps={{ disabled: !pendingCreation && (newPdfPending || Boolean(captureSelectionError(newFiles, pagesPerStudent))) }}
      >
        {!pendingCreation ? <>
          <Button disabled={creating} onClick={() => newFilesInput.current?.click()}>选择答卷文件</Button>
          <label>每人页数<Select disabled={creating} aria-label="新批次每人页数" value={pagesPerStudent} onChange={setPagesPerStudent} options={Array.from({ length: 20 }, (_, i) => ({ value: i + 1, label: `${i + 1} 页` }))} /></label>
          <CaptureFileOrderPreview files={newFiles} pages={pagesPerStudent} onPendingChange={setNewPdfPending} onChange={files => { if (!creating) setNewFiles(files); }} disabled={creating} />
        </> : null}
        <Form
          form={form}
          disabled={creating}
          layout="vertical"
          initialValues={{ source_type: "web_upload" }}
        >
          <section aria-label="导入记录名称"><h3>导入记录名称（选填）</h3><Form.Item
            name="name"
            label="批次名称"
            rules={[
              { required: true, message: "请输入批次名称" },
              { max: 120 },
            ]}
          >
            <Input placeholder="例如：期末考试第一扫描批次" />
          </Form.Item></section>
          <Form.Item
            name="source_type"
            hidden
            label="采集方式"
            rules={[{ required: true }]}
          >
            <Select
              options={[{ value: "web_upload", label: "网页文件导入" }]}
            />
          </Form.Item>
          {supplement ? <Form.Item name="reason" label="补传原因" rules={[{ required: true, whitespace: true, message: "请填写补传原因" }, { max: 300 }]}><Input.TextArea rows={3} maxLength={300} placeholder="例如：补交学生答卷或补扫遗漏页面" /></Form.Item> : null}
        </Form>
      </Modal>;
  };
  return (
    <div className="capture-batch-page">
      <section className="capture-records" aria-label="批次文件与处理记录">
      <div className="capture-records-heading">
        <div>
          <h2>批次文件与处理记录</h2>
          <p>{batches.length} 个批次 · 文件、质检、页面处理与学生匹配{uploading ? " · 正在上传" : ""}</p>
        </div>
        <Space wrap>
          <Button
            icon={<RefreshCw size={16} />}
            onClick={() => void Promise.all([loadBatches(), collection.load(), loadDetail(selectedId, true)])}
          >
            刷新
          </Button>
          <Button
            type="primary"
            icon={<Plus size={16} />}
            onClick={openNewBatch}
            disabled={!canCreateBatch}
          >
            {supplement ? "补传答卷" : "导入答卷"}
          </Button>
          <input ref={newFilesInput} aria-label="选择新批次答卷文件" type="file" hidden multiple accept=".pdf,.png,.jpg,.jpeg,.tif,.tiff" onChange={event => { const files = Array.from(event.target.files ?? []); if (files.length) { setNewFiles(files); setModalOpen(true); } event.target.value = ""; }} />
        </Space>
      </div>
      {replacement?.batchId === selectedId ? <Alert type="warning" showIcon message={`正在替代：${replacement.name}`} description={replacement.code === "upload_group_duplicate"
        ? "请先选择每名学生页数，再一次上传尚未成功导入的完整答卷，不能只补单页。此前成功组的答卷不要再次选择，它们会继续保留；本次卡住的选择在新文件完整登记后整组退出有效输入。"
        : "请先选择每名学生页数，再一次上传本次选择所对应的全部完整答卷。登记成功后，原选择的全部文件及其答卷将退出有效输入；新答卷仍需处理成功。"} action={<Button disabled={uploading || Boolean(pendingUpload)} onClick={() => setReplacement(undefined)}>取消替代</Button>} /> : null}
      {locationError ? <Alert type="warning" showIcon message={locationError} action={<Button onClick={() => setLocationAttempt(value => value + 1)}>重试定位</Button>} /> : null}
      {locationChoices.length > 1 ? <Alert type="info" message={`定位到 ${locationChoices.length} 份采集答卷，请选择本次核对对象`} description={<Select aria-label="选择名册对应采集答卷" style={{ minWidth: 320 }} value={locatedSubmission ? `${selectedId}:${locatedSubmission}` : undefined} options={locationChoices.map((choice, index) => ({ value: `${choice.batch.id}:${choice.submissionId}`, label: `${choice.batch.name} · 考号 ${choice.candidateNo || "待匹配"} · 答卷 ${index + 1}` }))} onChange={value => { const choice = locationChoices.find(item => `${item.batch.id}:${item.submissionId}` === value); if (choice) { setSelectedId(choice.batch.id); setLocatedBatch(choice.batch.id); setLocatedSubmission(choice.submissionId); setActiveTab(hashQueryParam("tab") || "matching"); setLocating(false); } }} />} /> : null}
        <div id="capture-processing-records" className="capture-records-body">
            <CaptureBatchWorkspace detail={detail} detailError={detailError} detailLoading={detailLoading} selectedId={selectedId}
              collectionPanel={collectionPanel}
              batchListPanel={<CaptureBatchListPanel batches={batches} selectedId={selectedId} setSelectedId={setSelectedId} disabled={uploading} hasMoreBatches={hasMoreBatches} loadingMoreBatches={loadingMoreBatches} loadMoreBatches={loadMoreBatches} />}
              batchListState={renderBatchListState()}
              loadDetail={loadDetail} canManage={canEditSelectedBatch} batchCanManage={batchCanManage} progressPercent={progressPercent}
              summary={summary} uploadProps={uploadProps} uploading={uploading} actioning={actioning} startProcessing={startProcessing}
              uploadLabel={replacement?.batchId === selectedId ? "上传替代答卷" : undefined}
              pagesPerStudent={pagesPerStudent} setPagesPerStudent={setPagesPerStudent} pendingUpload={pendingUpload} retryUpload={retryUpload} discardUpload={discardUpload}
              reopenBatch={reopenBatch} activeTab={activeTab} setActiveTab={setActiveTab}
              issuePages={issuePages} pageColumns={pageColumns} fileColumns={fileColumns}
              uploadRepair={() => { setActiveTab("files"); message.info("请按学生页序一次选择完整答卷重新导入；新选择独立分组，系统会保留原失败记录"); }}
              onOverride={(page) => { setQualityOverrideReason(""); setQualityOverridePage(page); }}
              processingProps={{ submissions, processingSummaries, resolveRegistration, setCorrectionRunId, startPageProcessing }}
              matchingProps={{ loading: matchingLoading, error: matchingError, onRetry: () => void loadMatching(selectedId), queue: matching,
                workspaceProps: { initialSubmissionId: locatedSubmission, canManage: batchCanManage, readOnlyReason: matchingReadOnlyReason(),
                  // 重开仍走已有原因确认弹窗，不能因学生待匹配就自动解除已完成批次的锁定。
                  onReopen: canEditSelectedBatch && detail?.batch.status === "completed" && detail.batch.id === selectedId && !detailLoading && !locating && !uploading && !detailError ? reopenBatch : undefined,
                  actioning, onPreview: showPage,
                  onConfirmStudent: matchStudent, onUnknown: markUnknown, onConfirmPage: matchPage, onSplit: splitSubmission, onMerge: mergeSubmissions } }} />
        </div>
      </section>
      <Modal
        title="核对本次答卷页序" open={Boolean(appendPreview)} okText="确认导入并处理" onCancel={() => setAppendPreview(undefined)} onOk={confirmAppendPreview}
        okButtonProps={{ disabled: appendPdfPending || !appendPreview || !batchCanManage || appendPreview.examId !== examId || appendPreview.batchId !== selectedId || Boolean(captureSelectionError(appendPreview.files, appendPreview.pages)) }}>
        {appendPreview && <CaptureFileOrderPreview files={appendPreview.files} pages={appendPreview.pages} onPendingChange={setAppendPdfPending} onChange={files => setAppendPreview(current => current ? { ...current, files } : current)} />}
      </Modal>
      <Modal
        title="人工放行质量误判"
        open={Boolean(qualityOverridePage)}
        okText="确认放行并继续处理"
        cancelText="取消"
        confirmLoading={actioning}
        okButtonProps={{ disabled: qualityOverrideReason.trim().length < 5 }}
        onCancel={() => {
          setQualityOverridePage(undefined);
          setQualityOverrideReason("");
        }}
        onOk={() => void confirmQualityOverride()}
      >
        <p>仅当你已查看原图并确认页面可正常阅卷时使用。系统会保留原质量问题、操作者、原因和时间，并立即恢复版面对齐任务。</p>
        <Input.TextArea
          value={qualityOverrideReason}
          rows={4}
          maxLength={500}
          placeholder="至少填写 5 个字，例如：人工核对字迹和答题区域清晰，倾斜不影响识别"
          onChange={(event) => setQualityOverrideReason(event.target.value)}
        />
      </Modal>
      <Modal
        title="重开采集批次"
        open={reopenOpen}
        okText="确认重开"
        cancelText="取消"
        confirmLoading={actioning}
        okButtonProps={{ disabled: !reopenReason.trim() }}
        onCancel={() => setReopenOpen(false)}
        onOk={() => void confirmReopen()}
      >
        <Input.TextArea value={reopenReason} rows={3} maxLength={300} placeholder="填写重开原因" onChange={(event) => setReopenReason(event.target.value)} />
      </Modal>
      {renderNewBatchModal()}
      <Modal
        title="答卷图片预览"
        open={Boolean(preview)}
        onCancel={() => {
          previewRequestRef.current += 1;
          if (preview?.url) URL.revokeObjectURL(preview.url);
          setPreview(undefined);
        }}
        footer={null}
        width={900}
      >
        {preview ? (
          <img
            className="capture-page-preview"
            src={preview.url}
            alt="答卷页面图片"
          />
        ) : null}
      </Modal>
    </div>
  );
}
