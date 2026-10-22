import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Drawer, Typography } from "antd";
import { usePageLoading, usePageVisible } from "./pageReadiness";
import {
  decideQuestionAppeal,
  getQuestionAppealAnswerImage,
  getQuestionAppealContext,
  listQuestionAppeals,
  resolveQuestionAppeal,
  startQuestionAppealReview,
  submitQuestionAppealReviewOpinion,
  type PublishedQuestionAppeal,
  type PublishedQuestionAppealContext
} from "../api/questionAppeals";
import { ApiClientError, getUserErrorMessage } from "../api/client";
import { listManagedUsers, type ManagedUser } from "../api/users";
import { listRegradeJobs, type RegradeJob } from "../api/scoreReleases";
import { listStudents } from "../api/org";
import { QuestionAppealActions, QuestionAppealEvidence, QuestionAppealFeedback, QuestionAppealIdentityHeader, QuestionAppealQueue, QuestionAppealResolution, QuestionAppealSummary, questionAppealBusinessSummary } from "./QuestionAppealViews";
import { questionAppealRubricPoints } from "./questionAppealPresentation";
import { QuestionAppealRegradeLaunch } from "./QuestionAppealRegradeLaunch";
import { useUnsavedChangesGuard } from "../router/useUnsavedChangesGuard";
import { QuestionAppealReviewOpinionPanel, type ReviewOpinionChoice } from "./QuestionAppealReviewOpinionPanel";

function messageFor(error: unknown) {
  return getUserErrorMessage(error, "操作失败，请稍后重试");
}

function reviewOpinionMessage(error: unknown) {
  if (error instanceof ApiClientError && error.code === "appeal_source_release_invalid") return "学校已更新当前公布成绩，意见未提交，填写内容已保留。请联系学校管理员重新核对本申请的成绩版本。";
  if (error instanceof ApiClientError && error.code === "resource_version_conflict") return "本申请已被其他人更新，意见未提交，填写内容已保留。请先复制说明，再刷新申请核对当前分派与处理状态。";
  return messageFor(error);
}

function canSubmitTeacherOpinion(appeal: PublishedQuestionAppeal | undefined, canManage: boolean, canWork: boolean, currentUserId: string) {
  // 教师只可对分派给本人且仍在复核中的申请提交意见；管理员走独立结论入口。
  return !canManage && canWork && Boolean(currentUserId) && appeal?.assigned_to === currentUserId && appeal.status === "under_review";
}

async function loadQuestionAppealReviewers(isCurrentRequest: () => boolean): Promise<ManagedUser[]> {
  const allUsers: ManagedUser[] = [];
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    const page = await listManagedUsers(cursor ? { limit: 100, cursor } : { limit: 100 });
    // 每页返回后核对刷新版本；过期请求不继续分页，调用方仍须在写入前复核版本。
    if (!isCurrentRequest()) return [];
    allUsers.push(...page.users);
    if (!page.has_more) break;
    if (!page.next_cursor || seenCursors.has(page.next_cursor)) throw new Error("复核人员分页异常，请刷新后重试");
    seenCursors.add(page.next_cursor); cursor = page.next_cursor;
  } while (cursor);
  return allUsers.filter((user) => user.status === "active" && user.roles.some((role) => ["teacher", "grader", "arbitrator"].includes(role)));
}

export function QuestionAppealWorkspace({ examId = "", canManage, canWork, canRead = canManage || canWork, canManageScores = false, canReadIdentities = false, examLabels = {}, currentUserId = "", personalScope = false, onNavigate, onChanged, onEditorStateChange }: Readonly<{
  examId?: string; canManage: boolean; canWork: boolean; canRead?: boolean; canManageScores?: boolean;
  currentUserId?: string; personalScope?: boolean; onNavigate?: (path: string) => void; onChanged?: () => void;
  canReadIdentities?: boolean; examLabels?: Readonly<Record<string, string>>;
  onEditorStateChange?: (state: { dirty: boolean; busy: boolean }) => void;
}>) {
  const { modal } = App.useApp();
  const [appeals, setAppeals] = useState<PublishedQuestionAppeal[]>([]);
  const [reviewers, setReviewers] = useState<ManagedUser[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [selectedId, setSelectedId] = useState("");
  const [context, setContext] = useState<PublishedQuestionAppealContext | null>(null);
  const [contextError, setContextError] = useState("");
  const pageVisible = usePageVisible();
  // 列表会自动打开首条申请；先等其依据或失败说明，不能把空队列和详情占位当作就绪。
  usePageLoading(canRead && (loading || Boolean(selectedId && !context && !contextError)));
  const [actioning, setActioning] = useState(false);
  const [assignedTo, setAssignedTo] = useState("");
  const [regradeJobID, setRegradeJobID] = useState("");
  const [notice, setNotice] = useState("");
  const [contextRevision, setContextRevision] = useState(0);
  const [answerImageURL, setAnswerImageURL] = useState("");
  const [publicResponse, setPublicResponse] = useState("");
  const [reviewOpinion, setReviewOpinion] = useState<ReviewOpinionChoice | "">("");
  const [suggestedScore, setSuggestedScore] = useState<number | undefined>();
  const [reviewExplanation, setReviewExplanation] = useState("");
  const [resolutionID, setResolutionID] = useState("");
  const [regradeJobs, setRegradeJobs] = useState<RegradeJob[]>([]);
  const [regradeRevision, setRegradeRevision] = useState(0);
  const [regradeError, setRegradeError] = useState("");
  const [draftTouched, setDraftTouched] = useState(false);
  const [studentLabels, setStudentLabels] = useState<Record<string, string>>({});
  const [identityError, setIdentityError] = useState("");
  const [reviewersError, setReviewersError] = useState("");
  const [summaryLoading, setSummaryLoading] = useState(false);
  const summaryRequestRef = useRef(0);
  const selectedIdRef = useRef(selectedId);
  selectedIdRef.current = selectedId;
  const busyRef = useRef(false);
  const refreshRequestRef = useRef(0);
  useUnsavedChangesGuard({ dirty: draftTouched, busy: actioning, description: "复核意见、给学生的回复或本题复评选择尚未提交，离开后无法恢复。" });
  useEffect(() => { onEditorStateChange?.({ dirty: draftTouched, busy: actioning }); }, [draftTouched, actioning, onEditorStateChange]);
  useEffect(() => () => { refreshRequestRef.current += 1; summaryRequestRef.current++; }, []);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 切申诉使旧汇总读取失效，不能让上一条申请的迟到结果结束本条加载。
  useEffect(() => { summaryRequestRef.current++; setSummaryLoading(false); }, [selectedId]);

  const changeSelection = (next: () => void) => {
    if (busyRef.current) return;
    if (!draftTouched) { next(); return; }
    modal.confirm({ title: "放弃未提交的复核内容？", content: "当前意见、回复和复评选择只保存在本页，离开后无法恢复。", okText: "放弃并继续", cancelText: "继续编辑", onOk: () => { setDraftTouched(false); next(); } });
  };

  const refresh = useCallback(async () => {
    if (!canRead) { setAppeals([]); return; }
    const requestID = ++refreshRequestRef.current;
    setLoading(true); setError("");
    try {
      const response = await listQuestionAppeals(examId);
      if (requestID !== refreshRequestRef.current) return;
      // 双角色账号的服务端读权限可能覆盖全校；教师视角仍只呈现分派给当前账号的任务。
      const scopedAppeals = personalScope ? response.appeals.filter((appeal) => appeal.assigned_to === currentUserId) : response.appeals;
      setAppeals(scopedAppeals);
      setSelectedId((current) => scopedAppeals.some((appeal) => appeal.id === current) ? current : scopedAppeals[0]?.id ?? "");
      if (canManage) {
        setReviewersError("");
        try {
          const activeReviewers = await loadQuestionAppealReviewers(() => requestID === refreshRequestRef.current);
          if (requestID !== refreshRequestRef.current) return;
          setReviewers(activeReviewers);
        } catch (cause) { if (requestID === refreshRequestRef.current) { setReviewers([]); setReviewersError(messageFor(cause)); } }
      }
    } catch (error_) { if (requestID === refreshRequestRef.current) setError(messageFor(error_)); }
    finally { if (requestID === refreshRequestRef.current) setLoading(false); }
  }, [canRead, canManage, examId, personalScope, currentUserId]);

  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => {
    setStudentLabels({}); setIdentityError("");
    if (!canReadIdentities || personalScope) return;
    let active = true;
    const ids = [...new Set(appeals.flatMap((appeal) => appeal.student_id ? [appeal.student_id] : []))];
    const batches = Array.from({ length: Math.ceil(ids.length / 200) }, (_, index) => ids.slice(index * 200, (index + 1) * 200));
    // 身份目录只为学校管理视角辅助核对对象；教师视角不请求学生姓名与学号。
    void Promise.all(batches.map((batch) => listStudents({ ids: batch, limit: 200 }))).then((pages) => {
      if (active) setStudentLabels(Object.fromEntries(pages.flatMap((page) => page.students.map((student) => [student.id, `${student.name} · ${student.student_no}`]))));
    }).catch((cause) => { if (active) setIdentityError(messageFor(cause)); });
    return () => { active = false; };
  }, [appeals, canReadIdentities, personalScope]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: contextRevision 只负责重新读取当前申诉；删除后操作成功也不会刷新详情。
  useEffect(() => {
    if (!selectedId) { setContext(null); return; }
    // 选择或刷新版本变化后，上一轮详情不能覆盖当前申诉和分派选择。
    // contextRevision 用于重读同一申诉，不能因 effect 内未读取它而删除依赖。
    let active = true;
    setContext(null); setContextError(""); setPublicResponse(""); setRegradeJobID(""); setResolutionID(""); setDraftTouched(false);
    setReviewOpinion(""); setSuggestedScore(undefined); setReviewExplanation("");
    void getQuestionAppealContext(selectedId).then((response) => { if (active) {
      setContext(response.context); setAssignedTo(response.context.appeal.assigned_to ?? ""); setPublicResponse(response.context.appeal.public_response ?? "");
      setReviewOpinion(response.context.appeal.review_opinion?.opinion ?? ""); setSuggestedScore(response.context.appeal.review_opinion?.suggested_score); setReviewExplanation(response.context.appeal.review_opinion?.explanation ?? "");
    } }).catch((error_) => { if (active) setContextError(messageFor(error_)); });
    return () => { active = false; };
  }, [selectedId, contextRevision]);
  useEffect(() => {
    // 开始下载新申请图像前清掉旧 URL，不能用旧图核对新申请。
    setAnswerImageURL("");
    if (!context?.answer_image_ready) { setAnswerImageURL(""); return; }
    // active 只阻止迟到图像写入界面；清理时撤销本轮已经创建的 URL。
    let active = true;
    let objectURL = "";
    void getQuestionAppealAnswerImage(context.appeal.id).then((asset) => {
      objectURL = URL.createObjectURL(asset.blob);
      if (active) setAnswerImageURL(objectURL);
      else URL.revokeObjectURL(objectURL);
    }).catch(() => { if (active) setAnswerImageURL(""); });
    return () => { active = false; if (objectURL) URL.revokeObjectURL(objectURL); };
  }, [context?.appeal.id, context?.answer_image_ready]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: regradeRevision 只重读复评任务，不触发当前申诉的未提交回复重置。
  useEffect(() => {
    if (!canManageScores || !context) { setRegradeJobs([]); return; }
    // regradeRevision 只重读任务列表，保留同一申请的公开回复；不能随列表刷新重置详情草稿。
    let active = true;
    setRegradeError(""); setRegradeJobs([]);
    void listRegradeJobs(context.appeal.exam_id).then((result) => {
      if (active) setRegradeJobs(result.regrade_jobs.filter((job) => {
        const submissions = job.selector?.submission_ids;
        const band = job.selector?.score_band;
        return job.status !== "cancelled" && job.question_id === context.appeal.question_id && job.source_release_id === context.appeal.source_release_id
          && (!submissions?.length || submissions.includes(context.appeal.submission_id))
          && (band?.min == null || context.appeal.source_score >= band.min)
          && (band?.max == null || context.appeal.source_score <= band.max);
      }));
    }).catch((cause) => { if (active) setRegradeError(messageFor(cause)); });
    return () => { active = false; };
  }, [canManageScores, context, regradeRevision]);

  const selected = useMemo(() => appeals.find((appeal) => appeal.id === selectedId), [appeals, selectedId]);
  const points = questionAppealRubricPoints(context?.rubric_snapshot);
  // 结论仅由管理员提交；教师通过独立意见入口移交，不以工作权限直接结案。
  const canDecide = canManage;
  const canSubmitOpinion = canSubmitTeacherOpinion(context?.appeal, canManage, canWork, currentUserId);
  const resolutionOptions = context?.release_history.filter((release) => release.id && release.source === "regrade" && release.status === "published" && release.version > context.appeal.source_release_version).map((release) => ({ value: release.id, label: `第 ${release.version} 版 · ${release.reason}` })) ?? [];
  const refreshSelected = async () => {
    // 先等列表和管理端人员刷新，再触发详情重读并通知父页面；此处不等待详情请求完成。
    await refresh(); setContextRevision((value) => value + 1); onChanged?.();
  };
  const rereadSelectedSummary = async () => {
    if (!context || busyRef.current || summaryLoading) return;
    const appealID = context.appeal.id;
    const summaryExamID = context.appeal.exam_id;
    const requestID = ++summaryRequestRef.current;
    setSummaryLoading(true);
    try {
      const response = await getQuestionAppealContext(appealID);
      if (requestID !== summaryRequestRef.current || selectedIdRef.current !== appealID || response.context.appeal.id !== appealID || response.context.appeal.exam_id !== summaryExamID) return;
      // 此重读只更新辨认任务的名称和学科；不能回填新版评分、分派或修订号而悄悄替换教师核对过的证据与输入。
      const summary = { exam_name: response.context.appeal.exam_name, exam_subject: response.context.appeal.exam_subject };
      setContext((current) => current?.appeal.id === appealID ? { ...current, appeal: { ...current.appeal, ...summary } } : current);
      setAppeals((current) => current.map((item) => item.id === appealID ? { ...item, ...summary } : item));
    } catch {
      // 名称读取失败仍保留冻结材料和草稿；缺名称的持久提示继续提供同页重读。
    } finally {
      if (requestID === summaryRequestRef.current) setSummaryLoading(false);
    }
  };
  const assign = async () => {
    if (!selected || !assignedTo || !canManage || busyRef.current) return;
    busyRef.current = true; setActioning(true);
    try {
      // 分派依据当前队列版本，独立加载的详情不能替代 selected.revision。
      await startQuestionAppealReview(selected.id, { assigned_to: assignedTo, expected_revision: selected.revision });
      setNotice("已分派给阅卷教师处理。"); await refreshSelected();
    } catch (error_) { setContextError(messageFor(error_)); }
    finally { busyRef.current = false; setActioning(false); }
  };
  const decide = async (decision: "reject" | "refer_regrade") => {
    if (!context || !canDecide || busyRef.current) return;
    if (!publicResponse.trim()) { setContextError("请填写给学生的复核回复。"); return; }
    if (decision === "refer_regrade" && (!canManageScores || !regradeJobs.some((job) => job.id === regradeJobID))) { setContextError("请先创建或选择同题同发布版本的复评任务。"); return; }
    const appealID = context.appeal.id;
    busyRef.current = true; setActioning(true);
    try {
      // 处理结论依据当前展示的详情版本，不使用可能已经刷新的队列版本。
      await decideQuestionAppeal(context.appeal.id, {
        decision,
        public_response: publicResponse.trim(),
        ...(decision === "refer_regrade" ? { regrade_job_id: regradeJobID.trim() } : {}),
        expected_revision: context.appeal.revision
      });
      if (selectedIdRef.current !== appealID) return;
      setDraftTouched(false); setNotice(decision === "reject" ? "已答复学生，原成绩保持不变。" : "已关联受治理的题目级复评任务。"); await refreshSelected();
    } catch (error_) { setContextError(messageFor(error_)); }
    finally { busyRef.current = false; setActioning(false); }
  };

  const submitOpinion = async () => {
    if (!context || !canSubmitOpinion || busyRef.current) return;
    if (!reviewOpinion || !reviewExplanation.trim()) { setContextError("请选择复核意见并填写依据与说明。"); return; }
    if (suggestedScore !== undefined && (!Number.isFinite(suggestedScore) || suggestedScore < 0 || suggestedScore > context.appeal.source_max_score)) { setContextError("建议分值必须在本题零分与满分之间。"); return; }
    const appealID = context.appeal.id;
    busyRef.current = true; setActioning(true); setContextError("");
    try {
      // 按教师实际核对的详情修订提交；冲突或当前成绩换版时保留输入，不自动改用新版重发意见。
      const response = await submitQuestionAppealReviewOpinion(appealID, { opinion: reviewOpinion, ...(suggestedScore === undefined ? {} : { suggested_score: suggestedScore }), explanation: reviewExplanation.trim(), expected_revision: context.appeal.revision });
      if (selectedIdRef.current !== appealID) return;
      setContext((current) => current?.appeal.id === appealID ? { ...current, appeal: response.appeal } : current);
      setAppeals((current) => current.map((item) => item.id === appealID ? response.appeal : item));
      setDraftTouched(false); setNotice(`${questionAppealBusinessSummary(response.appeal, examLabels)}：复核意见已保存并移交学校管理员。成绩尚未改变，请等待管理员继续处理。`); onChanged?.();
    } catch (cause) { setContextError(reviewOpinionMessage(cause)); }
    finally { busyRef.current = false; setActioning(false); }
  };

  const resolve = async () => {
    if (!context || !canManage || busyRef.current || !resolutionID || !publicResponse.trim()) return;
    busyRef.current = true; setActioning(true);
    try {
      await resolveQuestionAppeal(context.appeal.id, { new_release_id: resolutionID, public_response: publicResponse.trim(), expected_revision: context.appeal.revision });
      setDraftTouched(false); setNotice("结果已公布，学生可以在我的复核申请中查看回复和成绩版本。"); await refreshSelected();
    } catch (cause) { setContextError(messageFor(cause)); }
    finally { busyRef.current = false; setActioning(false); }
  };

  if (!canRead) return null;
  return <section className="question-appeal-workspace" aria-labelledby="question-appeal-title">
    <QuestionAppealSummary appeals={appeals} loading={loading} actioning={actioning} error={error} identityError={identityError} examLabels={examLabels} onRefresh={() => changeSelection(() => { void refreshSelected(); })} />
    <QuestionAppealQueue appeals={appeals} selectedId={selectedId} personalScope={personalScope} examLabels={examLabels} studentLabels={studentLabels} onSelect={(id) => changeSelection(() => setSelectedId(id))} />
    {/* Drawer 默认挂到 body；候选页面尚未显示时保持关闭，避免绕过隐藏容器提前弹出材料。 */}
    <Drawer width={760} open={pageVisible && Boolean(selectedId)} title={selected ? `${questionAppealBusinessSummary(selected, examLabels)}复核材料` : "复核材料"} onClose={() => changeSelection(() => setSelectedId(""))}>
      {contextError ? <Alert type="error" showIcon message="无法处理此申诉" description={contextError} /> : null}
      {!context && !contextError ? <Typography.Text type="secondary">正在加载发布版本依据…</Typography.Text> : null}
      {context ? <div className="question-appeal-context">
        <QuestionAppealIdentityHeader appeal={context.appeal} examLabels={examLabels} studentLabels={studentLabels} personalScope={personalScope} summaryLoading={summaryLoading} onReloadSummary={() => void rereadSelectedSummary()} />
        <QuestionAppealEvidence context={context} answerImageURL={answerImageURL} points={points} />
        <QuestionAppealFeedback notice={notice} reviewersError={reviewersError} publicResponse={context.appeal.public_response} />
        <QuestionAppealReviewOpinionPanel saved={context.appeal.review_opinion} pending={context.appeal.status === "under_review"} canSubmit={canSubmitOpinion} opinion={reviewOpinion} suggestedScore={suggestedScore} explanation={reviewExplanation} maxScore={context.appeal.source_max_score} busy={actioning}
          onOpinionChange={(value) => { setReviewOpinion(value); setDraftTouched(true); }} onScoreChange={(value) => { setSuggestedScore(value); setDraftTouched(true); }} onExplanationChange={(value) => { setReviewExplanation(value); setDraftTouched(true); }} onSubmit={() => void submitOpinion()} />
        <QuestionAppealActions status={context.appeal.status} canManage={canManage} assignedTo={assignedTo} reviewers={reviewers}
          canDecide={canDecide} canManageScores={canManageScores} publicResponse={publicResponse} onPublicResponseChange={(value) => { setPublicResponse(value); setDraftTouched(true); }} onReloadRegrades={() => setRegradeRevision((value) => value + 1)} regradeOptions={regradeJobs.map((job) => ({ value: job.id, label: `${job.reason_text} · ${job.affected_count} 份答卷` }))} regradeError={regradeError}
          actioning={actioning} regradeJobID={regradeJobID} onAssignedToChange={setAssignedTo} onRegradeJobIDChange={(id) => { setRegradeJobID(id); setDraftTouched(true); }}
          onAssign={() => { void assign(); }} onDecide={(decision) => { void decide(decision); }} />
        {canManage && canManageScores && context.appeal.status === "under_review" ? <QuestionAppealRegradeLaunch key={context.appeal.id} appeal={context.appeal} reviewers={reviewers} response={publicResponse} busy={actioning} onWorkingChange={(working) => { busyRef.current = working; setActioning(working); }} onCreated={(id) => { setRegradeJobID(id); setRegradeRevision((value) => value + 1); setDraftTouched(true); setNotice("复评任务已创建，请填写回复并关联任务，再到考试成绩工作台完成复评和发布。"); }} /> : null}
        {canManage && context.appeal.status === "upheld_pending_regrade" ? <QuestionAppealResolution options={resolutionOptions} resolutionID={resolutionID} publicResponse={publicResponse} actioning={actioning} onResolutionChange={(value) => { setResolutionID(value); setDraftTouched(true); }} onPublicResponseChange={(value) => { setPublicResponse(value); setDraftTouched(true); }} onResolve={() => void resolve()} /> : null}
        {canManageScores && onNavigate && ["under_review", "upheld_pending_regrade"].includes(context.appeal.status) ? <Button onClick={() => onNavigate(`/exams/${encodeURIComponent(context.appeal.exam_id)}/scores`)}>前往本考试成绩工作台完成复评与发布</Button> : null}
      </div> : null}
    </Drawer>
  </section>;
}
