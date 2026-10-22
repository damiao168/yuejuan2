import { type ReactNode, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { getResult, listQuestionAnnotations, studentPaperPageImageURL, type StudentQuestion, type StudentQuestionAnnotation, type StudentResult } from "../../api";
import { friendlyError, sessionExpired, studentReleaseChanged } from "../../errors";
import { useStudentSession } from "../../studentSession";

type PaperPage = NonNullable<StudentResult["paper_pages"]>[number];
type ScoreMark = Pick<StudentQuestion, "question_id" | "question_no" | "score" | "max_score" | "answer_geometry">;

export function PaperViewer({ examID, result, onViewCurrent, onViewTextFeedback }: Readonly<{ examID: string; result: StudentResult; onViewCurrent: () => void; onViewTextFeedback?: () => void }>) {
  const { submitting, isSubmitting } = useStudentSession();
  const ownPages = useMemo(() => result.paper_pages?.length ? result.paper_pages : fallbackPaperPages(result.questions ?? []), [result.paper_pages, result.questions]);
  const highPages = result.high_score_paper?.pages ?? [];
  const [highScore, setHighScore] = useState(false);
  const [pageIndex, setPageIndex] = useState(0);
  const [zoom, setZoom] = useState(1);
  const [releaseChanged, setReleaseChanged] = useState(false);
  const pages = highScore ? highPages : ownPages;
  const page = pages[Math.min(pageIndex, Math.max(0, pages.length - 1))];
  const pageQuestions = useMemo(() => (result.questions ?? []).filter((question) => question.page_no === page?.page_no), [page?.page_no, result.questions]);
  const scoreMarks = highScore ? (result.high_score_paper?.score_marks ?? []).filter((mark) => mark.page_no === page?.page_no) : pageQuestions;
  const markReleaseChanged = useCallback(() => setReleaseChanged(true), []);

  let pageContent: ReactNode;
  if (releaseChanged) {
    pageContent = <div className="paper-empty"><div><p role="alert">学校已更新当前成绩，请重新核对后查看答卷。未提交的复核草稿仍按原版本保留。</p><button type="button" className="secondary-button" disabled={submitting} onClick={() => { if (!submitting && !isSubmitting?.()) onViewCurrent(); }}>查看当前成绩并重新核对</button></div></div>;
  } else if (page) {
    // 页码、视图或发布改变时卸载旧读取，旧图片和批注的迟到结果不能提示新页失败。
    pageContent = <PaperPageEvidence key={`${result.release_id}:${highScore}:${page.page_no}:${page.question_id}`} examID={examID} releaseID={result.release_id} page={page} highScore={highScore} pageQuestions={pageQuestions} scoreMarks={scoreMarks} zoom={zoom} onReleaseChanged={markReleaseChanged} />;
  } else {
    pageContent = <PaperEmptyState hasQuestions={Boolean(result.questions?.length)} submitting={submitting} isSubmitting={isSubmitting} onViewTextFeedback={onViewTextFeedback} />;
  }

  return <section className="paper-viewer" aria-labelledby="paper-viewer-title">
    <header><h2 id="paper-viewer-title">答卷图像</h2></header>
    <div className="paper-toolbar">
      {page ? <>
      <div className="zoom-controls"><button type="button" disabled={releaseChanged} onClick={() => setZoom((value) => Math.min(1.6, value + .15))}>放大</button><button type="button" disabled={releaseChanged} onClick={() => setZoom(1)}>默认</button><button type="button" disabled={releaseChanged} onClick={() => setZoom((value) => Math.max(.7, value - .15))}>缩小</button></div>
      <div className="page-controls"><button type="button" disabled={releaseChanged || pageIndex <= 0} onClick={() => setPageIndex((value) => value - 1)}>上一页</button><label>第 <select disabled={releaseChanged} value={pageIndex} onChange={(event) => setPageIndex(Number(event.target.value))}>{pages.map((item, index) => <option key={`${item.page_no}-${index}`} value={index}>{item.page_no}</option>)}</select> 页</label><button type="button" disabled={releaseChanged || pageIndex >= pages.length - 1} onClick={() => setPageIndex((value) => value + 1)}>下一页</button></div>
      </> : null}
      <div className="paper-actions">{result.high_score_paper?.available ? <button type="button" className={highScore ? "high-score active" : "high-score"} disabled={releaseChanged} onClick={() => { setPageIndex(0); setHighScore((value) => !value); }}>{highScore ? "查看我的试卷" : "查看高分试卷"}</button> : <p>学校尚未提供可查看的优秀答卷。</p>}{page && !releaseChanged ? <a href={studentPaperPageImageURL(examID, page.question_id, highScore, result.release_id)} download>下载当前页</a> : null}</div>
    </div>
    <div className="paper-stage">
      {pageContent}
    </div>
  </section>;
}

function PaperEmptyState({ hasQuestions, submitting, isSubmitting, onViewTextFeedback }: Readonly<{
  hasQuestions: boolean; submitting: boolean; isSubmitting?: () => boolean; onViewTextFeedback?: () => void;
}>) {
  // 页内定位由父页滚动与聚焦，不修改当前考试地址，保留成绩页和未提交的复核内容。
  // 按钮禁用依赖渲染状态，点击时再查同步提交标记，避免提交刚开始就跳转。
  return <div className="paper-empty"><div><p>暂无可查看的答卷图像。可先查看题目文字反馈；需要原卷时请联系学校考试负责人。</p>{hasQuestions && onViewTextFeedback ? <button type="button" className="text-button" disabled={submitting} onClick={() => { if (!submitting && !isSubmitting?.()) onViewTextFeedback(); }}>查看题目文字反馈</button> : null}</div></div>;
}

function PaperPageEvidence({ examID, releaseID, page, highScore, pageQuestions, scoreMarks, zoom, onReleaseChanged }: Readonly<{
  examID: string; releaseID: string; page: PaperPage; highScore: boolean; pageQuestions: StudentQuestion[]; scoreMarks: ScoreMark[]; zoom: number; onReleaseChanged: () => void;
}>) {
  const { submitting, isSubmitting, reauthenticate } = useStudentSession();
  const [annotations, setAnnotations] = useState<StudentQuestionAnnotation[]>([]);
  const [annotationError, setAnnotationError] = useState("");
  const [annotationLoading, setAnnotationLoading] = useState(false);
  const [annotationAttempt, setAnnotationAttempt] = useState(0);
  const [imageUnavailable, setImageUnavailable] = useState(false);
  const [imageChecking, setImageChecking] = useState(false);
  const [imageAttempt, setImageAttempt] = useState(0);
  const [imageError, setImageError] = useState("");
  const [annotationNeedsLogin, setAnnotationNeedsLogin] = useState(false);
  const [imageNeedsLogin, setImageNeedsLogin] = useState(false);
  const lifecycleRef = useRef({ active: false, generation: 0 });
  const imageCheckRef = useRef(false);

  // 图片缓存失败可在首个被动 effect 前触发；提交阶段建立生存代次，避免遗漏首次错误。
  useLayoutEffect(() => {
    const lifecycle = lifecycleRef.current;
    lifecycle.active = true; lifecycle.generation += 1;
    // StrictMode 首帧重挂载会作废图片复核，但保留 state/ref；旧请求不能让重试永久锁住。
    if (imageCheckRef.current) {
      imageCheckRef.current = false;
      setImageChecking(false);
      setImageError("答卷图像暂时无法加载，请重试图像。");
    }
    return () => { lifecycle.active = false; lifecycle.generation += 1; };
  }, []);

  // biome-ignore lint/correctness/useExhaustiveDependencies: annotationAttempt 是教师批注失败后的显式重试触发，移除会让重试按钮只改计数而不重新请求。
  useEffect(() => {
    let active = true;
    const generation = lifecycleRef.current.generation;
    setAnnotations([]); setAnnotationError(""); setAnnotationNeedsLogin(false);
    if (highScore || !pageQuestions.length) { setAnnotationLoading(false); return () => { active = false; }; }
    setAnnotationLoading(true);
    void Promise.allSettled(pageQuestions.map((question) => listQuestionAnnotations(examID, question.question_id, releaseID).catch((reason) => {
      // 已知版本冲突须立即显示，不能等待同页另一道题的批注请求结束。
      if (active && lifecycleRef.current.active && generation === lifecycleRef.current.generation && studentReleaseChanged(reason)) onReleaseChanged();
      throw reason;
    }))).then((responses) => {
      if (!active || !lifecycleRef.current.active || generation !== lifecycleRef.current.generation) return;
      // 一道题确认发布变化，就停止整页展示；普通批注失败保留同版图片并给出明确重试。
      const failures = responses.filter((response) => response.status === "rejected");
      if (failures.some((response) => studentReleaseChanged(response.reason))) { onReleaseChanged(); return; }
      setAnnotations(responses.flatMap((response) => response.status === "fulfilled" ? response.value.annotations : []));
      if (failures.length) {
        const loginFailure = failures.find((response) => sessionExpired(response.reason));
        setAnnotationNeedsLogin(Boolean(loginFailure));
        setAnnotationError(loginFailure ? friendlyError(loginFailure.reason) : "本页部分教师批注暂时无法加载，请重试批注。");
      }
    }).finally(() => { if (active && lifecycleRef.current.active && generation === lifecycleRef.current.generation) setAnnotationLoading(false); });
    return () => { active = false; };
  }, [annotationAttempt, examID, highScore, onReleaseChanged, pageQuestions, releaseID]);

  const verifyFailedImage = async () => {
    if (!lifecycleRef.current.active || imageCheckRef.current) return;
    const generation = lifecycleRef.current.generation;
    const current = () => lifecycleRef.current.active && generation === lifecycleRef.current.generation;
    imageCheckRef.current = true;
    setImageUnavailable(true); setImageChecking(true); setImageError(""); setImageNeedsLogin(false);
    try {
      // img 错误事件没有 HTTP 状态；只比较当前成绩发布 ID，不把复核读取的新版成绩直接展示到旧页。
      // 本人与高分答卷共用这个版本检查，也适用于学校没有公开逐题结果的情况。
      const response = await getResult(examID);
      if (!current()) return;
      if (response.result.release_id !== releaseID) { onReleaseChanged(); return; }
      setImageError("答卷图像暂时无法加载，请重试图像。");
    } catch (reason) {
      if (!current()) return;
      if (studentReleaseChanged(reason)) { onReleaseChanged(); return; }
      setImageNeedsLogin(sessionExpired(reason));
      setImageError(sessionExpired(reason) ? friendlyError(reason) : "答卷图像暂时无法加载，当前也无法核对成绩版本，请稍后重试。");
    } finally {
      if (current()) { imageCheckRef.current = false; setImageChecking(false); }
    }
  };

  return <>
    {annotationLoading ? <p className="muted"><output>正在加载本页批注…</output></p> : null}
    {annotationError ? <p className="detail-error" role="alert">{annotationError} <button type="button" className="text-button" disabled={annotationLoading} onClick={() => setAnnotationAttempt((value) => value + 1)}>重试整页批注</button></p> : null}
    {(annotationNeedsLogin || imageNeedsLogin) ? <button type="button" className="secondary-button" disabled={submitting} onClick={() => { if (!submitting && !isSubmitting?.()) reauthenticate(); }}>重新登录</button> : null}
    {imageUnavailable ? <div className="paper-empty"><div>{imageChecking ? <p><output>答卷图像暂时不可用，正在核对成绩版本…</output></p> : <p className="detail-error" role="alert">{imageError}</p>}<button type="button" className="secondary-button" disabled={imageChecking} onClick={() => {
      if (imageCheckRef.current) return;
      setImageError("");
      setImageUnavailable(false);
      setImageAttempt((value) => value + 1);
    }}>重试答卷图像</button></div></div> :
      <div className="paper-page" style={{ width: `${zoom * 100}%` }}>
        <img key={imageAttempt} src={studentPaperPageImageURL(examID, page.question_id, highScore, releaseID)} alt={`${highScore ? "高分" : "本人"}试卷第 ${page.page_no} 页`} onError={() => void verifyFailedImage()} />
        {scoreMarks.map((question) => question.answer_geometry ? <span className={`paper-score-mark ${question.score < question.max_score ? "lost" : ""}`} key={question.question_id} style={{ left: `${Math.min(.96, question.answer_geometry.x + question.answer_geometry.width) * 100}%`, top: `${question.answer_geometry.y * 100}%` }}>{question.question_no}：{formatScore(question.score)}分（满分{formatScore(question.max_score)}分）</span> : null)}
        {!highScore ? annotations.map((annotation) => <span className={`paper-annotation ${annotation.type}`} key={annotation.id} title={annotation.content} style={{ left: `${annotation.geometry.x * 100}%`, top: `${annotation.geometry.y * 100}%`, width: `${annotation.geometry.width * 100}%`, height: `${annotation.geometry.height * 100}%` }}><span className="paper-annotation-text">{annotation.content}</span></span>) : null}
      </div>}
  </>;
}

function fallbackPaperPages(questions: StudentQuestion[]) {
  const byPage = new Map<number, PaperPage>();
  for (const question of questions) {
    if (question.page_no && !byPage.has(question.page_no)) byPage.set(question.page_no, { page_no: question.page_no, question_id: question.question_id, submission_page_id: question.submission_page_id });
  }
  return [...byPage.values()].sort((a, b) => a.page_no - b.page_no);
}

function formatScore(value: number) { return Number.isInteger(value) ? String(value) : value.toFixed(1); }
