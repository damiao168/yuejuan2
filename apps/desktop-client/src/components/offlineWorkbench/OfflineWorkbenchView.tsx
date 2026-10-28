import { useCallback, useEffect, useState, type FC, type ReactNode } from 'react';
import { Alert, Button, Checkbox, Empty, Form, Input, InputNumber, List, Modal, Space, Table, Tag, type TableColumnsType } from 'antd';
import { BookOpenCheck, Download, KeyRound, RefreshCw, Save, Send, Trash2 } from 'lucide-react';
import type { ReviewTaskContext } from '@edugrade/sdk';
import { AnswerDocumentViewer } from '../AnswerDocumentViewer';
import { hasDurableDesktopStore } from '../../lib/durableStore';
import type { OfflineDraftEnvelope } from '../../lib/offlineStore';
import { reviewTaskState, offlineSyncStatusLabels } from '../../statusLabels';
import type { AiGrade, OfflineGradeDraft, OfflineTaskPackage, ReviewTask } from '../../types';
import type { OfflineAnswerPreview, OfflineTaskPackageWithAnswer } from './types';
import { latestGrade, packageAnswerText } from './taskPackage';
import type { OfflineWorkbenchModel } from './useOfflineWorkbench';

// 页脚只提供按钮；点击时由本次回调读取操作锁，再决定是否允许放弃输入。
export function renderOfflineLeaveFooter(onDiscard: () => void, _originNode: ReactNode, { OkBtn, CancelBtn }: { OkBtn: FC; CancelBtn: FC }) {
  return <Space><CancelBtn /><Button danger onClick={onDiscard}>放弃未保存输入并离开</Button><OkBtn /></Space>;
}

export function OfflineWorkbenchStatusTags({ isOnline, hasKey }: Readonly<{ isOnline: boolean; hasKey: boolean }>) {
  return <Space wrap>
    <Tag color={isOnline ? "success" : "error"}>{isOnline ? "在线" : "离线"}</Tag>
    <Tag color={hasKey ? "success" : "warning"}>{hasDurableDesktopStore() ? "使用本机加密存储" : hasKey ? "开发密钥已设置" : "请设置开发密钥（至少 8 位）"}</Tag>
  </Space>;
}

export function MaterialSaveAlert({ pkg, packageSaved, draftSaved, hasImage }: Readonly<{
  pkg: OfflineTaskPackageWithAnswer;
  packageSaved: boolean;
  draftSaved: boolean;
  hasImage: boolean;
}>) {
  let materialDescription = "题目与评分细则来自任务冻结版本。";
  if (!pkg.question) materialDescription = "冻结题目与评分细则未获取。";
  else if (pkg.question.rubric?.status === "missing") materialDescription = "冻结评分细则缺失，任务包仅记录缺失状态。";
  const draftDescription = draftSaved ? "当前评分草稿已保存。" : "当前评分输入尚未保存。";
  const imageDescription = pkg.answerMaterial ? packageSaved ? "原件已随草稿加密保存，恢复后仍须显示核对" : "原件已准备，请保存草稿以供离线恢复" : hasImage ? "仅在本次会话可用，尚未完成离线准备" : "尚未加载";
  const answerDescription = packageAnswerText(pkg) || hasImage ? "已有可核对答卷材料。" : "没有可核对答卷材料，不能提交。";
  return <Alert
    type={packageSaved && draftSaved ? "success" : "warning"}
    showIcon
    message={packageSaved ? pkg.answerMaterial ? "任务材料与答卷原件已加密保存" : "任务材料已加密保存；答卷原件未保存" : "任务材料尚未保存"}
    description={`${materialDescription}${answerDescription}${draftDescription}答卷图片${imageDescription}。`}
  />;
}

export function taskColumns(selectTask: (id: string) => void, openTask: (id: string) => void, busy: boolean): TableColumnsType<ReviewTask> {
  return [
    { title: "匿名号", dataIndex: "anonymous_code" },
    { title: "题号", dataIndex: "question_no", width: 80 },
    { title: "状态", dataIndex: "status", width: 100, render: (value: string) => <Tag title={reviewTaskState(value).detail}>{reviewTaskState(value).label}</Tag> },
    {
      title: "操作",
      key: "action",
      width: 84,
      render: (_value, task) => (
        <Space><Button size="small" disabled={busy} onClick={() => selectTask(task.id)}>
          选择
        </Button><Button size="small" disabled={busy || !reviewTaskState(task.status).canGrade} onClick={() => openTask(task.id)}>打开阅卷</Button></Space>
      )
    }
  ];
}

export function PackageView({ pkg, imagePreview, onImageReadableChange }: Readonly<{
  pkg: OfflineTaskPackageWithAnswer | null;
  imagePreview: OfflineAnswerPreview | null;
  onImageReadableChange: (preview: OfflineAnswerPreview, readable: boolean) => void;
}>) {
  const [revealedSecondOpinion, setRevealedSecondOpinion] = useState(false);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 换题或重新下载材料都撤回第二意见展示，避免新独立评分沿用上一份材料的已揭示状态。
  useEffect(() => { setRevealedSecondOpinion(false); }, [pkg?.task.id, pkg?.downloadedAt]);
  const reportImageReadability = useCallback((readable: boolean) => {
    if (imagePreview) onImageReadableChange(imagePreview, readable);
  }, [imagePreview, onImageReadableChange]);
  if (!pkg) {
    return <Empty description="尚未下载任务包" />;
  }
  return (
    <div className="package-view">
      <div className="package-summary">
        <Tag color="blue">{pkg.task.anonymous_code}</Tag>
        <Tag title={reviewTaskState(pkg.task.status).detail}>{reviewTaskState(pkg.task.status).label}</Tag>
        <Tag>评分标准 {pkg.rubricVersion ?? "未记录"}</Tag>
        <Tag>{pkg.assessmentSource === "task_context" ? "任务冻结题目与细则" : "旧版细则来源未记录，请重新下载"}</Tag>
      </div>
      {pkg.warnings.length > 0 && <Alert type="warning" showIcon message="任务包不完整" description={pkg.warnings.join("；")} />}
      <div className="answer-preview">
        {imagePreview ? <AnswerDocumentViewer key={`${imagePreview.taskId}:${imagePreview.requestId}`} document={imagePreview} onReadableChange={reportImageReadability} /> : <Empty description={pkg.answerImageUrl ? "答案图片待下载/下载失败" : "任务未提供答卷图片入口"} />}
      </div>
      <h4>答题内容</h4>
      <pre>{packageAnswerText(pkg) || "未记录答卷文字"}</pre>
      <h4>任务题目</h4>
      <p>{pkg.question?.stem || "未记录题干"}</p>
      <h4>评分细则</h4>
      {pkg.question?.rubric?.status === "missing" ? <Alert type="warning" showIcon message="本任务缺少冻结评分细则，请联系管理员补核任务配置。" /> : null}
      <List
        size="small"
        dataSource={pkg.question?.rubric?.points ?? []}
        locale={{ emptyText: <Empty description="未获取评分标准" /> }}
        renderItem={(point) => (
          <List.Item>
            <span>{point.description}</span>
            <Tag>{point.score} 分</Tag>
          </List.Item>
        )}
      />
      <h4>AI 建议</h4>
      {latestGrade(pkg.aiGrades) ? (
        <p>
          建议分 {latestGrade(pkg.aiGrades)?.suggested_score}/{latestGrade(pkg.aiGrades)?.max_score}，置信度 {Math.round((latestGrade(pkg.aiGrades)?.confidence ?? 0) * 100)}%
        </p>
      ) : (
        <p className="muted">未获取 AI 建议分。</p>
      )}
      {pkg.aiSecondOpinion?.available && !pkg.aiSecondOpinion.score_prefill_allowed ? <>
        {/* 人工主导任务先独立核对答卷；辅助意见需明确展开，不能从其载荷恢复被禁止的建议分。 */}
        <Button onClick={() => setRevealedSecondOpinion((value) => !value)}>{revealedSecondOpinion ? "收起 AI 辅助意见" : "查看 AI 辅助意见"}</Button>
        {revealedSecondOpinion ? <OfflineSecondOpinionDetails metadata={pkg.aiSecondOpinion.metadata} /> : null}
      </> : null}
      {(pkg.aiCandidates?.length ?? 0) > 0 ? <>
        <h4>AI 识别候选</h4>
        <List size="small" dataSource={pkg.aiCandidates ?? []} renderItem={(candidate) => <List.Item>
          <div><Tag>{candidate.is_current ? "当前候选" : "历史候选"}</Tag><span>来源：{answerCandidateSource(candidate.source)}</span>
            {typeof candidate.confidence === "number" ? <span> · 置信度 {Math.round(candidate.confidence * 100)}%</span> : null}
            <pre>{candidate.display_text || "候选未提供可读文字"}</pre></div>
        </List.Item>} />
      </> : null}
    </div>
  );
}

export function OfflineSecondOpinionDetails({ metadata }: Readonly<{ metadata: NonNullable<ReviewTaskContext["ai_second_opinion"]>["metadata"] }>) {
  // 这里只展示已展开的说明和置信度；建议分是否可采用仍由任务上下文的预填权限决定。
  return <div>
    {typeof metadata.teacher_note === "string" ? <p>{metadata.teacher_note}</p> : null}
    {typeof metadata.student_feedback === "string" ? <p>{metadata.student_feedback}</p> : null}
    {typeof metadata.confidence === "number" ? <p>辅助意见置信度 {Math.round(metadata.confidence * 100)}%</p> : null}
  </div>;
}

export function DraftEditor({
  pkg,
  draft,
  setDraft,
  maxScore,
  latestAiGrade,
  disabled
}: Readonly<{
  pkg: OfflineTaskPackage | null;
  draft: OfflineGradeDraft;
  setDraft: React.Dispatch<React.SetStateAction<OfflineGradeDraft>>;
  maxScore: number;
  latestAiGrade?: AiGrade;
  disabled: boolean;
}>) {
  const [manualTotalTask, setManualTotalTask] = useState<string>();
  if (!pkg) {
    return <Empty description="下载任务包后填写草稿" />;
  }
  const points = pkg.question?.rubric?.points ?? [];
  const manualTotal = points.length === 0 || manualTotalTask === pkg.task.id;
  const pointsTotal = points.reduce((sum, point) => sum + (draft.rubricSelections[point.id] ?? 0), 0);
  const adjustedTotal = points.length > 0 && draft.score !== null && Math.abs(draft.score - pointsTotal) > 0.001;
  return (
    <Form layout="vertical" className="offline-draft-form" disabled={disabled}>
      <Form.Item label={`最终分 / ${maxScore || "未记录"} 分`}>
        <InputNumber disabled={disabled || !manualTotal} min={0} max={maxScore || undefined} value={draft.score} onChange={(value) => setDraft((current) => ({ ...current, score: value === null ? null : Number(value) }))} />
        {latestAiGrade && <span className="muted"> AI 建议：{latestAiGrade.suggested_score}</span>}
        {points.length > 0 ? <Checkbox checked={manualTotal} disabled={disabled} onChange={(event) => {
          setManualTotalTask(event.target.checked ? pkg.task.id : undefined);
          if (!event.target.checked) setDraft(current => ({ ...current, score: pointsTotal }));
        }}>调整总分</Checkbox> : null}
        {points.length > 0 && !manualTotal ? <p className="muted">修改评分点后自动汇总总分；恢复的原总分在修改前保留。</p> : null}
      </Form.Item>
      <div className="rubric-edit-list">
        {points.map((point) => (
          <label key={point.id}>
            <span>{point.description}</span>
            <InputNumber
              min={0}
              max={point.score}
              value={draft.rubricSelections[point.id] ?? 0}
              onChange={(value) =>
                setDraft((current) => {
                  const rubricSelections = { ...current.rubricSelections, [point.id]: value === null ? 0 : Number(value) };
                  // 仅教师主动修改评分点才汇总；加载历史草稿不改原总分，主动调整总分仍保留差异原因要求。
                  const score = manualTotal ? current.score : Math.round(points.reduce((sum, item) => sum + (rubricSelections[item.id] ?? 0), 0) * 1000) / 1000;
                  return { ...current, rubricSelections, score };
                })
              }
            />
          </label>
        ))}
      </div>
      {latestAiGrade && !latestAiGrade.mock && <Space wrap className="section-alert"><Button disabled={disabled} onClick={() => Modal.confirm({ title: "确认采用 AI 建议分？", content: `最终分将改为 ${latestAiGrade.suggested_score}。评分点与反馈保持原文，请人工核对评分依据。`, okText: "采用建议分", cancelText: "继续核对", onOk: () => setDraft((current) => current.taskId === pkg.task.id ? { ...current, score: latestAiGrade.suggested_score } : current) })}>采用 AI 建议分</Button>
        {latestAiGrade.student_feedback && <Button disabled={disabled} onClick={() => Modal.confirm({ title: "审读并采用 AI 学生反馈", content: <div><p>以下文字将替换学生可见反馈，分数和内部备注保持原文。发布后是否展示由学校发布策略决定。</p><p>{latestAiGrade.student_feedback}</p></div>, okText: "采用反馈并继续编辑", cancelText: "保留现有文字", onOk: () => setDraft((current) => current.taskId === pkg.task.id ? { ...current, studentFeedback: latestAiGrade.student_feedback ?? "" } : current) })}>审读 AI 反馈</Button>}</Space>}
      <Form.Item label="学生可见反馈（可选，发布后按学校策略展示）">
        <Input.TextArea rows={2} value={draft.studentFeedback} onChange={(event) => setDraft((current) => ({ ...current, studentFeedback: event.target.value }))} />
      </Form.Item>
      <details><summary>内部阅卷记录（可选）</summary>
      <Form.Item label="教师评语（阅卷记录，可选）">
        <Input.TextArea rows={3} value={draft.comments} onChange={(event) => setDraft((current) => ({ ...current, comments: event.target.value }))} />
      </Form.Item>
      <Form.Item label="内部备注（可选，阅卷教师及授权管理员可见）">
        <Input.TextArea rows={2} value={draft.privateNote} onChange={(event) => setDraft((current) => ({ ...current, privateNote: event.target.value }))} />
      </Form.Item>
      </details>
      {adjustedTotal || draft.reason ? <Form.Item label="人工调整/提交原因（总分与评分点合计不同时必填）">
        <Input value={draft.reason} placeholder="说明人工调整评分的依据；其他情况可选" onChange={(event) => setDraft((current) => ({ ...current, reason: event.target.value }))} />
      </Form.Item> : null}
    </Form>
  );
}

export function answerCandidateSource(source: string) {
  return ({ ocr: "文字识别", ocr_text: "识别文字", vlm: "图像识别", human: "人工核对", manual: "人工核对" } as Record<string, string>)[source] ?? source;
}

export function SyncTag({ status }: Readonly<{ status: OfflineDraftEnvelope["syncStatus"] }>) {
  let color = "default";
  if (status === "synced") color = "success";
  else if (status === "conflict") color = "warning";
  else if (status === "failed") color = "error";
  else if (status === "syncing") color = "processing";
  return <Tag color={color}>{offlineSyncStatusLabels[status]}</Tag>;
}

export function formatDate(value: string) {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString("zh-CN", { hour12: false });
}

// 展示层只调用控制器给出的动作；保存、同步和离开确认的顺序仍由同一控制器判断。
export function OfflineWorkbenchView({ model }: Readonly<{ model: OfflineWorkbenchModel }>) {
  const { isOnline, hasKey, pkg, packageSaved, draftSaved, hasReadableImage, offlineKey, setOfflineKey, loadingTasks, token, user, loadMyTasks, syncing, savingDraft, purgeExpired, taskError, setSelectedTaskId, downloadPackage, openTask, packageLoading, tasks, selectedTaskId, selectedTask, examNames, restorePackageImage, packageError, envelopes, loadDraft, imagePreview, onImageReadableChange, pendingCommand, alreadySynced, draft, setDraft, maxScore, latestAiGrade, hasAnswerMaterial, saveDraft, syncDraft, syncStatus, nextTask, syncMessage } = model;
  return (
    <div className="offline-workbench">
      <section className="panel full">
        <div className="offline-head">
          <div>
            <p className="eyebrow">离线评分草稿</p>
            <h3>离线评分草稿工作台</h3>
            <p>点击“加密保存草稿”保存当前任务与评分。</p>
          </div>
          <OfflineWorkbenchStatusTags isOnline={isOnline} hasKey={hasKey} />
        </div>
        <Alert
          type="info"
          showIcon
          message={
            hasDurableDesktopStore()
              ? "加密保存任务、冻结细则、评分草稿与已准备的答卷原件。单份原件限 2 MB；保存后可离线恢复，密钥由当前 Windows 用户的系统凭据库保护。超限或不支持的材料需联网查看。"
              : "浏览器开发模式使用显式密钥加密保存草稿与已准备的答卷原件；单份原件限 2 MB。生产扫描和上传恢复需在桌面端验证。"
          }
        />
        {pkg ? <MaterialSaveAlert pkg={pkg} packageSaved={packageSaved} draftSaved={draftSaved} hasImage={hasReadableImage} /> : null}
        <div className="offline-toolbar">
          {!hasDurableDesktopStore() && <Input.Password prefix={<KeyRound size={14} />} value={offlineKey} onChange={(event) => setOfflineKey(event.target.value)} placeholder="浏览器开发密钥，至少 8 位" />}
          <Button icon={<RefreshCw size={16} />} loading={loadingTasks} disabled={!token || !user} onClick={loadMyTasks}>
            获取我的任务
          </Button>
          <Button icon={<Trash2 size={16} />} disabled={syncing || savingDraft} onClick={purgeExpired}>
            清理过期已同步草稿
          </Button>
        </div>
        {taskError && <Alert className="section-alert" type="error" message={taskError} showIcon />}
      </section>

      <div className="offline-grid">
        <section className="panel">
          <div className="section-head compact">
            <div className="section-title">
              <span>
                <BookOpenCheck size={20} />
              </span>
              <div>
                <h3>我的阅卷任务</h3>
                <p>只读取分配给当前用户的真实任务。</p>
              </div>
            </div>
          </div>
          <label>选择任务<select aria-label="选择阅卷任务" value={selectedTaskId} disabled={syncing || savingDraft || packageLoading} onChange={event => setSelectedTaskId(event.target.value)}><option value="">请选择</option>{tasks.map(task => <option key={task.id} value={task.id}>{examNames[task.exam_id] || "本次考试"} · 第{task.question_no}题 · {task.anonymous_code}</option>)}</select></label>
          <Button type="primary" loading={packageLoading} disabled={!selectedTaskId || syncing || savingDraft || packageLoading || !token && !hasKey} onClick={() => void openTask()}>{envelopes.some(item => item.taskId === selectedTaskId) || pkg?.task.id === selectedTaskId ? "继续阅卷" : "开始阅卷"}</Button>
          {selectedTask ? <Alert className="section-alert" type="info" showIcon message={`当前选择：${selectedTask.anonymous_code} · 第 ${selectedTask.question_no ?? "待核对"} 题`} description={`${examNames[selectedTask.exam_id] ?? "考试名称尚未加载，请联网刷新任务核对"}。${reviewTaskState(selectedTask.status).detail}。打开其他任务前会核对当前草稿是否保存。`} /> : null}
          <details><summary>重新下载与任务目录（恢复工具）</summary>
          <Table rowKey="id" size="small" columns={taskColumns(setSelectedTaskId, (id) => { setSelectedTaskId(id); void openTask(id); }, !token || syncing || savingDraft || packageLoading)} dataSource={tasks} rowClassName={(task) => task.id === selectedTaskId ? "selected-review-task" : ""} pagination={{ pageSize: 6, showSizeChanger: false }} locale={{ emptyText: <Empty description="暂无任务" /> }} />
          <Button className="section-button" type="primary" icon={<Download size={16} />} loading={packageLoading} disabled={!selectedTaskId || !token || syncing || savingDraft} onClick={() => void downloadPackage()}>
            {selectedTask ? `下载任务包：${selectedTask.anonymous_code} · 第 ${selectedTask.question_no ?? "待核对"} 题` : "下载任务包"}
          </Button>
          </details>
          <Button className="section-button" icon={<RefreshCw size={16} />} loading={packageLoading} disabled={!pkg || !token || !isOnline || syncing || savingDraft || packageLoading} onClick={() => void restorePackageImage()}>
            恢复答卷图片
          </Button>
          {packageError && <Alert className="section-alert" type="error" message={packageError} showIcon />}
        </section>

        <section className="panel">
          <div className="section-head compact">
            <div className="section-title">
              <span>
                <Save size={20} />
              </span>
              <div>
                <h3>本地加密草稿</h3>
                <p>同步结果和冲突状态在这里可见。</p>
              </div>
            </div>
          </div>
          <List
            size="small"
            dataSource={envelopes}
            locale={{ emptyText: <Empty description="暂无本地草稿" /> }}
            renderItem={(item) => {
              const messageSuffix = item.syncMessage ? " / " + item.syncMessage : "";
              return (
                <List.Item actions={[<Button key="load" size="small" disabled={!hasKey || syncing || savingDraft} onClick={() => void loadDraft(item.taskId)}>加载</Button>]}>
                  <List.Item.Meta
                    title={
                      <Space wrap>
                        <span>{item.anonymousCode}</span>
                        <SyncTag status={item.syncStatus} />
                      </Space>
                    }
                    description={`${formatDate(item.savedAt)} / 过期 ${formatDate(item.expiresAt)}${messageSuffix}`}
                  />
                </List.Item>
              );
            }}
          />
        </section>
      </div>

      <div className="offline-grid workbench">
        <section className="panel">
          <PackageView pkg={pkg} imagePreview={imagePreview} onImageReadableChange={onImageReadableChange} />
        </section>
        <section className="panel">
          {pendingCommand && !alreadySynced ? <Alert type="warning" showIcon message="上次提交结果尚未确认，分数和评语暂不可修改。再次同步会先查询上次提交结果。" /> : null}
          <DraftEditor pkg={pkg} draft={draft} setDraft={setDraft} maxScore={maxScore} latestAiGrade={latestAiGrade} disabled={syncing || savingDraft || packageLoading || alreadySynced || pendingCommand} />
          <Space className="offline-actions" wrap>
            <Button icon={<Save size={16} />} loading={savingDraft} disabled={!pkg || !hasKey || syncing || savingDraft || packageLoading || alreadySynced || pendingCommand} onClick={() => void saveDraft()}>
              加密保存草稿
            </Button>
            <Button type="primary" icon={<Send size={16} />} loading={syncing} disabled={!pkg || (!hasAnswerMaterial && !pendingCommand) || !hasKey || syncing || savingDraft || packageLoading || alreadySynced} onClick={() => void syncDraft()}>
              同步提交
            </Button>
            <SyncTag status={syncStatus} />
            {alreadySynced && nextTask ? <Button disabled={!token || syncing || savingDraft || packageLoading} onClick={() => { setSelectedTaskId(nextTask.id); void openTask(nextTask.id); }}>{`下一份：${nextTask.anonymous_code}`}</Button> : null}
          </Space>
          {syncMessage && <Alert className="section-alert" type={syncStatus === "conflict" || syncStatus === "failed" ? "warning" : "info"} message={syncMessage} showIcon />}
        </section>
      </div>
    </div>
  );
}
