// 申诉表单保留草稿、会话提交锁和服务端回执顺序；证据失效时不能继续提交。
import { SubmitEvent, PointerEvent, useCallback, useEffect, useRef, useState } from "react";
import { createQuestionAppeal, listQuestionAppeals, PortalApiError, studentQuestionAnswerImageURL, type StudentQuestionAppeal, type StudentQuestion, type SelectedAppealRegion } from "../../api";
import { appealDraftKey, isAppealDraftStored, readAppealDraft, removeAppealDraft, writeAppealDraft, type AppealDraft } from "../../appealDrafts";
import { useStudentSession } from "../../studentSession";
import { friendlyError, sessionExpired } from "../../errors";
import { appealReasonLabels } from "./appealPresentation";
import { appealStatusLabel } from "./AppealStatus";

function AppealDraftNotice({ stored }: Readonly<{ stored: boolean }>) {
  // 这里只展示存储结果；同步写入和离开保护仍由表单及 App 执行。
  return <p className={stored ? "muted" : "detail-error"}><output aria-live="polite">{stored ? "草稿已保存在当前标签页会话，切题、返回或重新登录后可恢复；退出登录将清除。" : "浏览器未允许保存草稿，当前输入仅在本页保留。刷新或关闭前请先复制说明。"}</output></p>;
}

export function QuestionAppealForm({ examID, releaseID, releaseVersion, question, allowedReasonCodes, onSubmitted, onImageUnavailable, evidenceChecking = false, evidenceUnavailable = false, isEvidenceBlocked, onSourceUnavailable }: Readonly<{
  examID: string;
  releaseID: string;
  releaseVersion: number;
  question: Pick<StudentQuestion, "question_id" | "question_no">;
  allowedReasonCodes: string[];
  onSubmitted: () => void;
  evidenceChecking?: boolean;
  evidenceUnavailable?: boolean;
  isEvidenceBlocked?: () => boolean;
  onSourceUnavailable?: (reason: unknown) => void;
  onImageUnavailable?: () => void;
}>) {
  const { draftOwner, submitting: sessionSubmitting, isSubmitting, setSubmitting: setSessionSubmitting, reauthenticate } = useStudentSession();
  // 服务端将空白名单解释为不限制原因；学生端必须按同一规则提供六种已有原因。
  const availableReasons = (allowedReasonCodes.length ? allowedReasonCodes : Object.keys(appealReasonLabels)).filter((code) => appealReasonLabels[code]);
  const storageKey = draftOwner ? appealDraftKey(draftOwner, examID, releaseID, question.question_id) : "";
  const [draft, setDraft] = useState<AppealDraft>(() => {
    const saved = storageKey ? readAppealDraft(storageKey) : undefined;
    return { reasonCode: saved && availableReasons.includes(saved.reasonCode) ? saved.reasonCode : availableReasons[0] ?? "", reason: saved?.reason ?? "", ...(saved?.selectedRegion ? { selectedRegion: saved.selectedRegion } : {}) };
  });
  const draftRef = useRef(draft);
  const [draftStored, setDraftStored] = useState(() => !storageKey || isAppealDraftStored(storageKey));
  const updateDraft = (patch: Partial<AppealDraft>) => {
    const next = { ...draftRef.current, ...patch, questionNo:question.question_no, releaseVersion };
    draftRef.current = next;
    setDraft(next);
    // 在输入事件中同步存储，不能等 effect；快速切题或刷新时最后一次输入也必须属于原题原版本。
    if (storageKey) setDraftStored(writeAppealDraft(storageKey, next));
  };
  const { reasonCode, reason, selectedRegion } = draft;
  const [submitting, setSubmitting] = useState(false);
  const submissionStartedRef = useRef(false);
  const [error, setError] = useState("");
  const [loginRequired, setLoginRequired] = useState(false);
  const [closed, setClosed] = useState(false);
  const [duplicateDetected, setDuplicateDetected] = useState(false);
  const [existingLoading, setExistingLoading] = useState(true);
  const [existingError, setExistingError] = useState("");
  const [existingAppeal, setExistingAppeal] = useState<StudentQuestionAppeal | null>(null);

  const checkExisting = useCallback(async () => {
    setExistingLoading(true); setExistingError("");
    try {
      const response = await listQuestionAppeals(examID);
      const existing = response.appeals.find((appeal) => appeal.source_release_id === releaseID && appeal.question_id === question.question_id) ?? null;
      setExistingAppeal(existing);
      if (existing && storageKey) removeAppealDraft(storageKey);
    } catch (reason) { setExistingError(friendlyError(reason, "appeal")); setLoginRequired(sessionExpired(reason)); }
    finally { setExistingLoading(false); }
  }, [examID, question.question_id, releaseID, storageKey]);
  useEffect(() => {
    let active = true;
    setExistingLoading(true);
    void listQuestionAppeals(examID).then((response) => {
      if (!active) return;
      const existing = response.appeals.find((appeal) => appeal.source_release_id === releaseID && appeal.question_id === question.question_id) ?? null;
      setExistingAppeal(existing);
      if (existing && storageKey) removeAppealDraft(storageKey);
    }).catch((reason) => { if (active) { setExistingError(friendlyError(reason, "appeal")); setLoginRequired(sessionExpired(reason)); } }).finally(() => { if (active) setExistingLoading(false); });
    return () => { active = false; };
  }, [examID, question.question_id, releaseID, storageKey]);

  if (existingAppeal) {
    return <output className="question-appeal-confirmation"><strong>{appealStatusLabel(existingAppeal.status)}</strong><span>学校已收到本题复核申请。</span><span>本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。</span><a href="#my-question-appeals" onClick={(event) => { event.preventDefault(); document.getElementById("my-question-appeals")?.scrollIntoView({ behavior: "smooth" }); }}>查看我的申请</a>{evidenceUnavailable ? <span>此申请对应第 {releaseVersion} 版成绩，当前成绩需重新核对。</span> : null}</output>;
  }
  if (availableReasons.length === 0) {
    return <output className="question-appeal-confirmation" aria-live="polite" aria-atomic="true"><strong>暂时无法申请复核</strong><span>学校配置的复核原因当前不可用，请联系学校考试负责人核对设置。</span></output>;
  }
  const submit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    // 图片失败后的版本复核可能尚未重绘；直接提交事件也必须读取同步保护。
    if (submissionStartedRef.current || isSubmitting?.() || evidenceChecking || evidenceUnavailable || isEvidenceBlocked?.() || existingLoading || existingError || closed || duplicateDetected) return;
    submissionStartedRef.current = true;
    setSubmitting(true);
    setSessionSubmitting(true);
    setError("");
    setLoginRequired(false);
    try {
      const response = await createQuestionAppeal(examID, {
        source_release_id: releaseID,
        question_id: question.question_id,
        reason_code: reasonCode,
        reason: reason.trim(),
        ...(selectedRegion ? { selected_region: selectedRegion } : {})
      });
      setExistingAppeal(response.appeal);
      if (storageKey) removeAppealDraft(storageKey);
      onSubmitted();
    } catch (error_) {
      setError(friendlyError(error_));
      setLoginRequired(sessionExpired(error_));
      onSourceUnavailable?.(error_);
      if (error_ instanceof PortalApiError && error_.code === "appeal_window_closed") setClosed(true);
      if (error_ instanceof PortalApiError && error_.code === "appeal_already_filed") { setDuplicateDetected(true); void checkExisting(); onSubmitted(); }
    } finally {
      submissionStartedRef.current = false;
      setSubmitting(false);
      setSessionSubmitting(false);
    }
  };
  if (evidenceUnavailable) {
    // 证据失效时仍保留只读原说明；复制不恢复旧圈选，也不能重发旧版申请。
    // 已发 POST 不跟着证据换版消失；这里只显示原版回执，不提供旧版重发或把草稿迁移到新版。
    return <QuestionAppealReceipt hasSubmission={submitting || Boolean(error) || duplicateDetected} questionNo={question.question_no} releaseVersion={releaseVersion} submitting={submitting} sessionSubmitting={sessionSubmitting} error={error} storageKey={storageKey} reason={reason} selectedRegion={selectedRegion} draftStored={draftStored} existingError={existingError} loginRequired={loginRequired} reauthenticate={reauthenticate} duplicateDetected={duplicateDetected} existingLoading={existingLoading} checkExisting={checkExisting} />;
  }
  return <form className="question-appeal-form" onSubmit={submit}>
    <QuestionAppealFields examID={examID} questionID={question.question_id} releaseID={releaseID} releaseVersion={releaseVersion} storageKey={storageKey} reason={reason} selectedRegion={selectedRegion} reasonCode={reasonCode} draftStored={draftStored} evidenceChecking={evidenceChecking} existingLoading={existingLoading} existingError={existingError} checkExisting={checkExisting} submitting={submitting} sessionSubmitting={sessionSubmitting} onImageUnavailable={onImageUnavailable} updateDraft={updateDraft} availableReasons={availableReasons} error={error} loginRequired={loginRequired} reauthenticate={reauthenticate} duplicateDetected={duplicateDetected} existingAppeal={existingAppeal} closed={closed} />
  </form>;
}

interface QuestionAppealFeedbackProps {
  releaseVersion: number;
  submitting: boolean;
  sessionSubmitting: boolean;
  error: string;
  storageKey: string;
  reason: string;
  selectedRegion?: SelectedAppealRegion;
  draftStored: boolean;
  existingError: string;
  loginRequired: boolean;
  reauthenticate: () => void;
  duplicateDetected: boolean;
  existingLoading: boolean;
  checkExisting: () => Promise<void>;
}

// 只展示原版本回执；提交事实、草稿清理与重查状态仍由父表单持有。
function QuestionAppealReceipt({ questionNo, hasSubmission, releaseVersion, submitting, sessionSubmitting, error, storageKey, reason, selectedRegion, draftStored, existingError, loginRequired, reauthenticate, duplicateDetected, existingLoading, checkExisting }: Readonly<QuestionAppealFeedbackProps & { questionNo: string; hasSubmission: boolean }>) {
  const originalReasonRef = useRef<HTMLTextAreaElement>(null);
  return <section className="question-appeal-form" aria-label={`第 ${questionNo} 题复核申请回执`}>
    <h3>{hasSubmission ? "复核申请回执" : "原版本复核草稿"}</h3>
    <p>{hasSubmission ? "此申请" : "此未提交草稿"}对应第 {releaseVersion} 版成绩。当前成绩需重新核对，原版本草稿不会自动迁移。</p>
    {submitting ? <p><output aria-live="polite">正在提交第 {releaseVersion} 版的复核申请，请等待回执。</output></p> : null}
    {error ? <p className="detail-error" role="alert">{error}</p> : null}
    {!submitting && storageKey && (reason || selectedRegion) ? <AppealDraftNotice stored={draftStored} /> : null}
    {/* 存储被拒绝时原说明只在内存中；只读取回区保留原题原版文本，不恢复过期证据或提供旧版重发。 */}
    {reason ? <>
      <label>第 {questionNo} 题 · 第 {releaseVersion} 版原说明（只读）<textarea ref={originalReasonRef} readOnly rows={5} value={reason} /></label>
      <button type="button" className="secondary-button" onClick={() => { originalReasonRef.current?.focus(); originalReasonRef.current?.select(); }}>选中原说明</button>
      <p>选中后按 Ctrl+C 或 ⌘C 复制，手机可长按复制。请自行保存原说明，再重新核对当前成绩。</p>
    </> : null}
    {existingError ? <p role="alert">无法确认是否已提交。{existingError}</p> : null}
    {loginRequired ? <button type="button" className="secondary-button" disabled={submitting || sessionSubmitting} onClick={reauthenticate}>重新登录</button> : null}
    {duplicateDetected ? <button type="button" className="text-button" disabled={existingLoading} onClick={() => void checkExisting()}>查看已有申请记录</button> : null}
  </section>;
}

interface QuestionAppealFieldsProps extends QuestionAppealFeedbackProps {
  examID: string;
  questionID: string;
  releaseID: string;
  reasonCode: string;
  evidenceChecking: boolean;
  onImageUnavailable?: () => void;
  updateDraft: (patch: Partial<AppealDraft>) => void;
  availableReasons: string[];
  existingAppeal: StudentQuestionAppeal | null;
  closed: boolean;
}

// 输入区没有独立草稿或提交状态，避免展示组件重挂载后改变原题原版本的请求边界。
function QuestionAppealFields({ examID, questionID, releaseID, releaseVersion, storageKey, reason, selectedRegion, reasonCode, draftStored, evidenceChecking, existingLoading, existingError, checkExisting, submitting, sessionSubmitting, onImageUnavailable, updateDraft, availableReasons, error, loginRequired, reauthenticate, duplicateDetected, existingAppeal, closed }: Readonly<QuestionAppealFieldsProps>) {
  return <>
    <h3>申请复核</h3>
    <p>每道题在同一版成绩下只能提交一次。学校将按第 {releaseVersion} 版成绩核对原卷和评分标准。</p>
    {storageKey && (reason || selectedRegion) ? <AppealDraftNotice stored={draftStored} /> : null}
    {evidenceChecking ? <p><output aria-live="polite">正在核对答卷所属成绩版本，完成前暂不能提交。</output></p> : null}
    {existingLoading ? <p><output aria-live="polite">正在核对已有复核申请…</output></p> : null}
    {existingError ? <p role="alert">无法确认是否已提交。{existingError} <button type="button" className="text-button" onClick={() => void checkExisting()}>重试申请记录</button></p> : null}
    <fieldset disabled={submitting} className="appeal-form-fields">
    <AppealRegionSelector
      onImageUnavailable={onImageUnavailable}
      imageURL={studentQuestionAnswerImageURL(examID, questionID, releaseID)}
      value={selectedRegion}
      onChange={(selectedRegion) => updateDraft({ selectedRegion })}
    />
    <label>复核原因<select value={reasonCode} onChange={(event) => updateDraft({ reasonCode: event.target.value })}>{availableReasons.map((code) => <option key={code} value={code}>{appealReasonLabels[code]}</option>)}</select></label>
    <label>具体说明<textarea required minLength={10} maxLength={500} value={reason} onChange={(event) => updateDraft({ reason: event.target.value })} placeholder="请具体说明哪一步、哪一评分点或哪段识别内容需要复核（10—500字）" /></label>
    </fieldset>
    {error ? <p className="detail-error" role="alert">{error}</p> : null}
    {loginRequired ? <button type="button" className="secondary-button" disabled={submitting || sessionSubmitting} onClick={reauthenticate}>重新登录</button> : null}
    {duplicateDetected && !existingAppeal ? <button type="button" className="text-button" onClick={() => void checkExisting()}>查看已有申请记录</button> : null}
    <button className="secondary-button" type="submit" disabled={submitting || evidenceChecking || existingLoading || Boolean(existingError) || closed || duplicateDetected || reason.trim().length < 10}>{submitting ? "正在提交…" : "确认提交（仅一次）"}</button>
  </>;
}

function AppealRegionSelector({ imageURL, value, onChange, onImageUnavailable }: Readonly<{
  imageURL: string;
  value?: SelectedAppealRegion;
  onChange: (value: SelectedAppealRegion | undefined) => void;
  onImageUnavailable?: () => void;
}>) {
  const { submitting } = useStudentSession();
  const [imageState, setImageState] = useState<"loading" | "ready" | "unavailable">("loading");
  const [start, setStart] = useState<{ x: number; y: number } | null>(null);
  const [pointMode, setPointMode] = useState(false);

  const point = (event: PointerEvent<HTMLDivElement>) => {
    const bounds = event.currentTarget.getBoundingClientRect();
    return {
      x: Math.max(0, Math.min(1, (event.clientX - bounds.left) / bounds.width)),
      y: Math.max(0, Math.min(1, (event.clientY - bounds.top) / bounds.height))
    };
  };
  const regionFrom = (origin: { x: number; y: number }, end: { x: number; y: number }): SelectedAppealRegion => ({
    coordinate_space: "canonical_image_normalized",
    x: Math.min(origin.x, end.x), y: Math.min(origin.y, end.y),
    width: Math.abs(end.x - origin.x), height: Math.abs(end.y - origin.y)
  });
  const begin = (event: PointerEvent<HTMLDivElement>) => {
    // fieldset 不会禁用 div 的指针事件；提交中保持圈选内容与发送的草稿一致。
    if (submitting || imageState !== "ready") return;
    if (pointMode) {
      if (!start) setStart(point(event));
      else { const region = regionFrom(start, point(event)); setStart(null); if (region.width >= .01 && region.height >= .01) onChange(region); }
      return;
    }
    event.currentTarget.setPointerCapture(event.pointerId);
    setStart(point(event));
  };
  const finish = (event: PointerEvent<HTMLDivElement>) => {
    if (submitting) { setStart(null); return; }
    if (pointMode) return;
    if (!start) return;
    const region = regionFrom(start, point(event));
    setStart(null);
    if (region.width >= 0.01 && region.height >= 0.01) onChange(region);
  };
  return <section className="appeal-region-selector" aria-labelledby="appeal-region-title">
    <div><h4 id="appeal-region-title">圈选争议区域（可选）</h4><p>可拖动框选，也可依次点击区域的两个对角；未圈选也可以提交。</p></div>
    <label><input type="checkbox" checked={pointMode} onChange={(event) => { setPointMode(event.target.checked); setStart(null); }} /> 使用两点点击框选</label>
    {pointMode && start ? <p><output>起点已选，请点击区域的另一角。</output></p> : null}
    <div className="appeal-region-image-wrap">
      <div className="appeal-region-image">
      <img src={imageURL} alt="本题答题区域" onLoad={() => setImageState("ready")} onError={() => { setImageState("unavailable"); onImageUnavailable?.(); }} />
      {imageState === "ready" ? <div
        className="appeal-region-canvas"
        onPointerDown={begin}
        onPointerUp={finish}
        onPointerCancel={() => setStart(null)}
        aria-label="在答题图上框选争议区域"
        role="application"
        aria-disabled={submitting}
      >
        {value ? <span className="appeal-region-box" style={{ left: `${value.x * 100}%`, top: `${value.y * 100}%`, width: `${value.width * 100}%`, height: `${value.height * 100}%` }} /> : null}
      </div> : null}
      </div>
      {imageState === "loading" ? <span className="appeal-region-loading">正在加载本题答题区域…</span> : null}
    </div>
    {imageState === "unavailable" ? <p className="detail-error">本题答题图暂时不可用，仍可通过文字说明提交复核。</p> : null}
    <button type="button" className="secondary-button" disabled={imageState !== "ready"} onClick={() => onChange({ coordinate_space: "canonical_image_normalized", x: 0, y: 0, width: 1, height: 1 })}>选择整个答题区域</button>
    {value ? <details><summary>精确调整区域（百分比）</summary><div className="region-coordinate-fields">{(["x", "y", "width", "height"] as const).map((key) => <label key={key}>{{ x: "距左侧", y: "距顶部", width: "宽度", height: "高度" }[key]}<input type="number" min={key === "x" || key === "y" ? 0 : 1} max={100} value={Math.round(value[key] * 100)} onChange={(event) => { const next = { ...value, [key]: Math.max(key === "x" || key === "y" ? 0 : .01, Math.min(1, Number(event.target.value) / 100)) }; next.x = Math.min(.99, next.x); next.y = Math.min(.99, next.y); next.width = Math.min(next.width, 1 - next.x); next.height = Math.min(next.height, 1 - next.y); onChange(next); }} /></label>)}</div></details> : null}
    {value ? <div className="appeal-region-summary"><span>已圈选争议区域</span><button type="button" className="text-button" onClick={() => onChange(undefined)}>清除</button></div> : null}
  </section>;
}
