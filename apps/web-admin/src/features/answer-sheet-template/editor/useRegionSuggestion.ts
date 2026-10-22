import { useEffect, useRef, useState, type Dispatch, type RefObject } from "react";
import { App } from "antd";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { getUserErrorMessage } from "../../../api/client";
import type { LayoutRegion, TemplateEditorCandidate, TemplateLayout } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import type { TemplateLayoutAction } from "./templateLayoutReducer";
import { detectAnswerSheetFrames } from "./answerSheetImageRegions";
import { matchAnswerSheetRegions, type SheetWord } from "./answerSheetRegionMatching";
import { collectUnassignedFrames, mergeUnassignedFrames, type UnassignedRegions } from "./unassignedAnswerSheetRegions";
import type { RemotePageAnalyzer } from "./analyzeTemplatePage";
import type { TemplateFillGradingGroup } from "../../../api/answerSheetTemplateAnalysis";
import { validObjectiveBox } from "./objectiveGridGeometry";
import { candidateMaps, commitPageAnalysis, isReplaceableSuggestion, type PageAnalysisCommit } from "./regionLifecycle";
import { normalizeIdentityRegionLabel } from "./identityRegionFields";

function clamp(value: number, min = 0, max = 1) { return Math.min(max, Math.max(min, value)); }
function roundCoordinate(value: number) { return Number(value.toFixed(6)); }

type NormalizedQuestion = { question: Question; key: string };
type QuestionAnchor = { question: Question; top: number };
type PDFPage = Awaited<ReturnType<PDFDocumentProxy["getPage"]>>;
type AnswerSheetOCR = Awaited<ReturnType<typeof import("./answerSheetOCR")["createAnswerSheetOCR"]>>;

function collectPageAnchors(content: Awaited<ReturnType<PDFPage["getTextContent"]>>, height: number, normalized: NormalizedQuestion[], unmatched: Set<string>): QuestionAnchor[] {
  const anchors: QuestionAnchor[] = [];
  for (const raw of content.items) {
    if (!("str" in raw) || !("transform" in raw)) continue;
    const text = String(raw.str).replace(/\s+/g, "").toLowerCase();
    const candidate = normalized.find(({ question, key }) => unmatched.has(question.id)
      && (text === key || text.startsWith(`${key}.`)
        || text.startsWith(`${key}、`) || text.startsWith(`${key}．`)));
    if (!candidate) continue;
    const transform = raw.transform as number[];
    // PDF 文字坐标从底部起算；区域编辑器使用从顶部起算的 0～1 页面比例。
    anchors.push({
      question: candidate.question,
      top: clamp(1 - ((transform[5] || 0) + Math.abs(transform[3] || transform[0] || 12)) / height)
    });
    unmatched.delete(candidate.question.id);
  }
  return anchors.sort((a, b) => a.top - b.top);
}

export function useRegionSuggestion({
  pdfRef, questions, readonly, dispatchLayout, sourceKey, analysisRevision, imageUrl, coveredQuestionIDs, coveredRegions, analyzePage, editorLayout, sourceHash
}: {
  pdfRef: RefObject<PDFDocumentProxy | undefined>;
  questions: Question[];
  readonly: boolean;
  dispatchLayout: Dispatch<TemplateLayoutAction>;
  sourceKey?: string;
  analysisRevision?: number;
  imageUrl?: string;
  coveredQuestionIDs?: Set<string | undefined>;
  coveredRegions?: Map<number, LayoutRegion[]>;
  analyzePage?: RemotePageAnalyzer;
  editorLayout?: TemplateLayout;
  sourceHash?: string;
}) {
  const { message } = App.useApp();
  const [suggestingRegions, setSuggestingRegions] = useState(false);
  const [suggestionProgress, setSuggestionProgress] = useState<{ completed: number; total: number }>();
  const [suggestionError, setSuggestionError] = useState<string>();
  const [suggestionModel, setSuggestionModel] = useState<string>();
  const [suggestionTone, setSuggestionTone] = useState<"info" | "warning" | "error">("info");
  const [unassignedRegions, setUnassignedRegions] = useState<UnassignedRegions>(() => new Map());
  const candidateRef = useRef(unassignedRegions);
  const [objectiveGroups, setObjectiveGroups] = useState<UnassignedRegions>(() => new Map());
  const [fillGradingGroups, setFillGradingGroups] = useState<Map<number, TemplateFillGradingGroup[]>>(() => new Map());
  const objectiveRef = useRef(objectiveGroups);
  const assigningQuestions = useRef(new Set<string>());
  const generation = useRef(0);
  const busy = useRef(false);
  const cancelOCR = useRef<(() => void) | undefined>(undefined);
  const latestCovered = useRef(coveredQuestionIDs);
  latestCovered.current = coveredQuestionIDs;
  const latestRegions = useRef(coveredRegions);
  latestRegions.current = coveredRegions;
  const latestLayout = useRef(editorLayout);
  latestLayout.current = editorLayout;
  const scopeIdentity = `${sourceKey ?? "legacy"}:${questions.map(q => `${q.id}:${q.question_no}:${q.question_type}`).join("|")}`;
  const objectiveIdentity = `${scopeIdentity}:${analysisRevision ?? 0}`;
  const identity = `${objectiveIdentity}:${readonly}`;
  const hasEditorLayout = Boolean(editorLayout);
  const editorCandidateState = editorLayout?.editor_state;
  const currentIdentity = useRef(identity);
  if (currentIdentity.current !== identity) {
    currentIdentity.current = identity;
    generation.current++;
    busy.current = false;
  }
  // biome-ignore lint/correctness/useExhaustiveDependencies: 身份或只读状态改变时必须取消旧请求，不能删去触发身份。
  useEffect(() => {
    setSuggestingRegions(false); setSuggestionProgress(undefined); setSuggestionError(undefined); setSuggestionModel(undefined);
    cancelOCR.current?.();
    return () => { generation.current++; cancelOCR.current?.(); };
  }, [identity]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 候选只在账户、来源或题目清单改变时清空，临时只读保留人工候选。
  useEffect(() => {
    candidateRef.current = new Map();
    setUnassignedRegions(candidateRef.current);
    setFillGradingGroups(new Map());
    assigningQuestions.current.clear();
  }, [scopeIdentity]);
  // 旧调用没有持久草稿时，大区只存在 hook 内；版本变化后删除这份临时建议。
  // biome-ignore lint/correctness/useExhaustiveDependencies: 大区版本边界独立于普通候选来源，临时只读不删除。
  useEffect(() => {
    if (editorLayout) return;
    objectiveRef.current = new Map();
    setObjectiveGroups(objectiveRef.current);
  }, [objectiveIdentity, Boolean(editorLayout)]);
  useEffect(() => {
    if (!hasEditorLayout) return;
    const candidates = sourceHash && editorCandidateState?.source_sha256 === sourceHash ? editorCandidateState.candidates : [];
    candidateRef.current = candidateMaps(candidates, "unassigned"); setUnassignedRegions(candidateRef.current);
    objectiveRef.current = candidateMaps(candidates, "objective_group"); setObjectiveGroups(objectiveRef.current);
  }, [hasEditorLayout, editorCandidateState, sourceHash]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 正式区域更新后解除本地双击占位；占位不替代最新覆盖集合。
  useEffect(() => { assigningQuestions.current.clear(); }, [coveredQuestionIDs]);

  const discardSuggestedRegion = (candidateId: string) => {
    if (readonly || busy.current || currentIdentity.current !== identity) return;
    // 清空一页最后的候选时同时删除页键，避免页面仍把空 Map 条目当成未保存候选。
    const next = new Map([...candidateRef.current].flatMap(([pageNo, regions]) => {
      const remaining = regions.filter(region => region.id !== candidateId);
      return remaining.length ? [[pageNo, remaining] as const] : [];
    }));
    candidateRef.current = next;
    setUnassignedRegions(next);
    if (editorLayout) dispatchLayout({ type: "discardCandidate", candidateId });
  };
  const discardObjectiveGroup = (candidateId: string) => {
    if (readonly || busy.current || currentIdentity.current !== identity) return;
    const next = new Map([...objectiveRef.current].flatMap(([pageNo, groups]) => {
      const remaining = groups.filter(group => group.id !== candidateId);
      return remaining.length ? [[pageNo, remaining] as const] : [];
    }));
    objectiveRef.current = next;
    setObjectiveGroups(next);
    if (editorLayout) dispatchLayout({ type: "discardCandidate", candidateId });
  };
  const assignSuggestedRegion = (candidateId: string, questionId: string) => {
    if (readonly || busy.current || currentIdentity.current !== identity) return;
    const question = questions.find(item => item.id === questionId);
    if (!question) return;
    if (latestCovered.current?.has(questionId) || assigningQuestions.current.has(questionId)) {
      message.warning("此题已有答题区域，请先核对或删除原区域后再关联");
      return;
    }
    const entry = [...candidateRef.current].find(([, regions]) => regions.some(region => region.id === candidateId));
    const candidate = entry?.[1].find(region => region.id === candidateId);
    if (!entry || !candidate) return;
    assigningQuestions.current.add(questionId);
    // 教师重新选择用途即完成此框纠错；保留原来源和诊断，重试也不能覆盖这次人工决定。
    dispatchLayout({ type: "acceptSuggestedRegion", pageNo: entry[0], questionId,
      region: { ...candidate, question_id: questionId, label: question.question_no, review_state: "edited" } });
    discardSuggestedRegion(candidateId);
  };
  const assignSuggestedIdentity = (candidateId: string, label: string) => {
    if (readonly || busy.current || currentIdentity.current !== identity) return;
    const entry = [...candidateRef.current].find(([, regions]) => regions.some(region => region.id === candidateId));
    const candidate = entry?.[1].find(region => region.id === candidateId);
    if (!entry || !candidate) return;
    const normalizedLabel = normalizeIdentityRegionLabel(label);
    if (latestLayout.current?.pages.find(page => page.page_no === entry[0])?.identity_regions
      .some(region => normalizeIdentityRegionLabel(region.label) === normalizedLabel)) {
      message.warning("本页已有此个人信息区域，请先核对或删除原区域后再设置"); return;
    }
    dispatchLayout({ type: "acceptSuggestedIdentity", pageNo: entry[0], region: { ...candidate,
      label: normalizedLabel, question_id: undefined, review_state: "edited" } });
    candidateRef.current = new Map([...candidateRef.current].map(([pageNo, regions]) => [pageNo, regions.filter(region => region.id !== candidateId)]));
    setUnassignedRegions(candidateRef.current);
  };

  const suggestQuestionRegions = async () => {
    const pdf = pdfRef.current;
    if ((!pdf && !imageUrl) || readonly || (!questions.length && !sourceKey)) {
      message.warning("当前文件没有可分析的 PDF 页面，或模板已锁定");
      return;
    }
    if (busy.current) return;
    busy.current = true;
    const requestGeneration = generation.current;
    const expectedLayout = latestLayout.current;
    const isCurrent = () => generation.current === requestGeneration && currentIdentity.current === identity;
    setSuggestingRegions(true);
    setSuggestionError(undefined);
    setSuggestionModel(undefined);
    setSuggestionTone("info");
    const total = pdf?.numPages ?? 1;
    setSuggestionProgress({ completed: 0, total });
    let ocr: AnswerSheetOCR | undefined;
    let ocrInitialization: Promise<AnswerSheetOCR> | undefined;
    const abort = new AbortController();
    cancelOCR.current = () => { abort.abort(); void ocr?.terminate(); };
    try {
      const anchors = new Map<number, Array<{ question: Question; top: number }>>();
      const used = new Set<string>(editorLayout ? editorLayout.pages.flatMap(page => page.question_regions
        .filter(region => !isReplaceableSuggestion(region)).flatMap(region => region.question_id ? [region.question_id] : []))
        : [...(coveredQuestionIDs ?? [])].filter((id): id is string => Boolean(id)));
      const unmatched = new Set(questions.filter(q => !used.has(q.id)).map((question) => question.id));
      const imageSuggestions = new Map<number, LayoutRegion[]>();
      const identitySuggestions = new Map<number, LayoutRegion[]>();
      const registrationSuggestions = new Map<number, LayoutRegion[]>();
      const fillGroupSuggestions = new Map<number, TemplateFillGradingGroup[]>();
      const objectiveSuggestions = new Map<number, LayoutRegion[]>();
      const unassigned = new Map<number, LayoutRegion[]>();
      const candidateMetadata = new Map<string, Pick<TemplateEditorCandidate, "role" | "question_no">>();
      const failedPages: number[] = [];
      const remoteFailedPages: number[] = [];
      const remoteNotices: string[] = [];
      const remoteModels = new Set<string>();
      const successfulPages: number[] = [];
      const retainLocalFillPages: number[] = [];
      let detectedFrames = 0;
      // 先匹配较长题号，避免 1.1 的文字锚点被较短的题号 1 抢占。
      const normalized = questions.map((question) => ({
        question, key: question.question_no.replace(/\s+/g, "").toLowerCase()
      })).sort((a, b) => b.key.length - a.key.length);
      for (let pageIndex = 1; pageIndex <= total; pageIndex += 1) {
        if (!isCurrent()) return;
        try {
        const page = pdf ? await pdf.getPage(pageIndex) : undefined;
        if (analyzePage) {
          const { renderAnswerSheetPage } = await import("./answerSheetOCR");
          const canvas = await renderAnswerSheetPage(page, imageUrl);
          if (!isCurrent()) return;
          // 本地框线只作为几何建议；未知模板逐页交给多模态判断题号，不能被看似完整的 OCR 配对短路。
          let frames: ReturnType<typeof detectAnswerSheetFrames> = [];
          try {
            const context = canvas.getContext("2d", { willReadFrequently: true });
            if (context) frames = detectAnswerSheetFrames(context.getImageData(0, 0, canvas.width, canvas.height));
          } catch { /* 本地框线读取失败仍可发送整页图像，由后台提取候选。 */ }
          detectedFrames += frames.length;
          const regions: LayoutRegion[] = [];
          const identityRegions: LayoutRegion[] = [];
          const remoteUnassigned: LayoutRegion[] = [];
          let retainLocalFallback = true;
          try {
            const remote = await analyzePage({ canvas, pageNo: pageIndex, frames, signal: abort.signal });
            if (!isCurrent()) return;
            retainLocalFallback = remote.retainLocalFallback !== false;
            if (remote.processingSucceeded !== false) successfulPages.push(pageIndex);
            else remoteFailedPages.push(pageIndex);
            if (remote.retainLocalFillCandidates) retainLocalFillPages.push(pageIndex);
            remoteUnassigned.push(...remote.unassigned);
            for (const candidate of remote.unassignedCandidates ?? []) {
              candidateMetadata.set(candidate.id, { role: candidate.role, question_no: candidate.question_no });
            }
            identityRegions.push(...(remote.identityRegions ?? []));
            if (remote.registrationMarks) registrationSuggestions.set(pageIndex, remote.registrationMarks);
            if (remote.fillGradingGroups) fillGroupSuggestions.set(pageIndex, remote.fillGradingGroups);
            // 选择题大框仅用于后续核对网格，不能以一道题的身份进入可保存区域或完成数。
            const objective = (remote.objectiveGroups ?? []).filter(validObjectiveBox)
              .map(group => ({ ...group, question_id: undefined, label: "选择题区域" }));
            if (objective.length) objectiveSuggestions.set(pageIndex, objective);
            for (const region of remote.regions) {
              // 一题当前只能保存一个 ROI；原有人工区域及跨页同题的第二块只能保留为待核对候选。
              if (!region.question_id || used.has(region.question_id)) {
                remoteUnassigned.push({ ...region, question_id: undefined, label: "待核对区域", semantic_check: "needs_review",
                  reason_codes: [...new Set([...(region.reason_codes ?? []), ...(region.question_id ? ["SEMANTIC_MULTIPART_UNSUPPORTED"] : [])])] });
                continue;
              }
              used.add(region.question_id);
              unmatched.delete(region.question_id);
              regions.push(region);
            }
            if (remote.notice) remoteNotices.push(remote.notice);
            if (remote.modelName) {
              remoteModels.add(remote.modelName);
              setSuggestionModel([...remoteModels].join("、"));
            }
          } catch (error) {
            if (!isCurrent()) return;
            // 模型失败不能退回质量较差的本地题号关联；几何框独立保留，等待重试或人工确认。
            remoteFailedPages.push(pageIndex);
            const reason = getUserErrorMessage(error, "");
            if (reason) remoteNotices.push(`第 ${pageIndex} 页：${reason}`);
          }
          if (regions.length) imageSuggestions.set(pageIndex, regions);
          if (identityRegions.length) identitySuggestions.set(pageIndex, identityRegions);
          const protectedOnPage = (latestRegions.current?.get(pageIndex) ?? []).filter(region => !editorLayout || !isReplaceableSuggestion(region));
          const coveredOnPage = [...regions, ...identityRegions, ...protectedOnPage,
            ...(!editorLayout ? objectiveRef.current.get(pageIndex) ?? [] : []), ...(objectiveSuggestions.get(pageIndex) ?? [])];
          const localUnassigned = retainLocalFallback ? collectUnassignedFrames(frames, coveredOnPage) : [];
          const combined = mergeUnassignedFrames(new Map([[pageIndex, localUnassigned]]),
            new Map([[pageIndex, remoteUnassigned]]), new Map([[pageIndex, coveredOnPage]]));
          unassigned.set(pageIndex, combined.get(pageIndex) ?? []);
          setSuggestionProgress({ completed: pageIndex, total });
          continue;
        }
        const viewport = page?.getViewport({ scale: 1 });
        // 文字层损坏不等于图像不可读，扫描识别仍尝试渲染该页。
        const content = page ? await page.getTextContent().catch(error => {
          if (!sourceKey) throw error;
          return undefined;
        }) : undefined;
        if (!isCurrent()) return;
        const pageAnchors = content && viewport ? collectPageAnchors(content, viewport.height, normalized,
          sourceKey ? new Set(unmatched) : unmatched) : [];
        // 没有sourceKey的调用只保留原文字层路径；真实页面始终遍历全部页并分析边框。
        if (sourceKey) {
          const { renderAnswerSheetPage } = await import("./answerSheetOCR");
          const canvas = await renderAnswerSheetPage(page, imageUrl);
          if (!isCurrent()) return;
          const context = canvas.getContext("2d", { willReadFrequently: true });
          if (!context) throw new Error("浏览器无法读取答题卡图像");
          const frames = detectAnswerSheetFrames(context.getImageData(0, 0, canvas.width, canvas.height));
          detectedFrames += frames.length;
          let words: SheetWord[] = [];
          if (content && viewport) {
            words = content.items.flatMap(raw => {
              if (!("str" in raw) || !("transform" in raw)) return [];
              const transform = raw.transform;
              const height = Math.abs(transform[3] || transform[0] || 12);
              return [{ text: String(raw.str), x: transform[4] / viewport.width,
                y: 1 - (transform[5] + height) / viewport.height, width: raw.width / viewport.width,
                height: height / viewport.height, confidence: 100, source: "pdf_text" as const }];
            });
          }
          let regions = matchAnswerSheetRegions(frames, words, questions, used);
          if (frames.length && unmatched.size && (words.length === 0 || regions.length < frames.length)) {
            try {
              // 单次整份扫描共享初始化结果，失败也保留；避免每一页重新等待同一次资源故障。
              // 用户重新发起扫描时会创建新Promise，后续页面仍能独立使用文字层和边框匹配。
              ocrInitialization ??= import("./answerSheetOCR").then(({ createAnswerSheetOCR }) => createAnswerSheetOCR(abort.signal));
              ocr = await ocrInitialization;
              if (!isCurrent()) return;
              words = await ocr.recognize(canvas, frames);
              if (!isCurrent()) return;
              regions.push(...matchAnswerSheetRegions(frames, words, questions, used));
            } catch {
              if (!isCurrent()) return;
              // OCR失败不撤销本页已由文字层和边框配对的候选区域，缺失部分按失败页提示。
              failedPages.push(pageIndex);
            }
          }
          if (regions.length) {
            imageSuggestions.set(pageIndex, regions);
            for (const region of regions) unmatched.delete(region.question_id!);
          }
          const coveredOnPage = [...regions, ...(latestRegions.current?.get(pageIndex) ?? [])];
          const localUnassigned = collectUnassignedFrames(frames, coveredOnPage);
          unassigned.set(pageIndex, localUnassigned);
          // 图片有闭合边框时不再用整页宽的题号启发式覆盖它；无法配对的框留待核对。
          if (frames.length || regions.length) {
            setSuggestionProgress({ completed: pageIndex, total });
            continue;
          }
        }
        if (pageAnchors.length) {
          anchors.set(pageIndex, pageAnchors);
          for (const anchor of pageAnchors) { unmatched.delete(anchor.question.id); used.add(anchor.question.id); }
        }
        successfulPages.push(pageIndex);
        setSuggestionProgress({ completed: pageIndex, total });
        } catch (pageError) {
          if (!isCurrent()) return;
          if (!sourceKey) throw pageError;
          // 一个页面无法解码或 OCR 失败时继续后页；已成功的结果可直接使用，失败页单独提示。
          failedPages.push(pageIndex);
          setSuggestionProgress({ completed: pageIndex, total });
        }
      }
      if (!isCurrent()) return;
      if (editorLayout && sourceHash && expectedLayout && analyzePage) {
        // 识别等待期间允许人工修改；整次结果作为一个编辑提交，过期结果不再修改任何页面。
        if (latestLayout.current !== expectedLayout) {
          const notice = "识别等待期间模板已有修改，本次结果未应用；当前修改已保留，可保存后重新识别";
          setSuggestionError(notice); setSuggestionTone("warning"); message.warning(notice); return;
        }
        const annotate = (region: LayoutRegion): LayoutRegion => ({ origin: region.suggestion_source === "multimodal_layout" ? "model" : "local_detector",
          review_state: "suggested", semantic_check: "not_evaluated", ...region });
        const commit: PageAnalysisCommit = { sourceHash, successfulPages, retainLocalFillPages,
          registrationMarks: registrationSuggestions, pages: new Map([...imageSuggestions]
          .map(([pageNo, regions]) => [pageNo, regions.map(annotate)])), identities: new Map([...identitySuggestions]
          .map(([pageNo, regions]) => [pageNo, regions.map(annotate)])), candidates: [
          ...[...unassigned].flatMap(([pageNo, regions]) => regions.map(region => ({ id: region.id, page_no: pageNo,
            role: "uncertain" as const, ...candidateMetadata.get(region.id), region: annotate(region) }))),
          ...[...objectiveSuggestions].flatMap(([pageNo, regions]) => regions.map(region => ({ id: region.id, page_no: pageNo,
            role: "objective_group" as const, region: annotate(region) })))
        ] };
        const next = commitPageAnalysis(expectedLayout, commit);
        candidateRef.current = candidateMaps(next.editor_state?.candidates ?? [], "unassigned");
        objectiveRef.current = candidateMaps(next.editor_state?.candidates ?? [], "objective_group");
        setUnassignedRegions(candidateRef.current); setObjectiveGroups(objectiveRef.current);
        setFillGradingGroups(previous => new Map([
          ...[...previous].filter(([pageNo]) => !successfulPages.includes(pageNo)), ...fillGroupSuggestions
        ]));
        if (successfulPages.length) dispatchLayout({ type: "commitAnalysisPages", commit, expectedLayout });
        const pending = next.editor_state?.candidates.filter(candidate => candidate.region.review_state !== "discarded").length ?? 0;
        const matched = new Set(next.pages.flatMap(page => page.question_regions.flatMap(region => region.question_id ? [region.question_id] : [])));
        const missing = questions.filter(question => !matched.has(question.id)).length;
        const failures = [...new Set([...failedPages, ...remoteFailedPages])];
        const notice = [failures.length ? `第 ${failures.join("、")} 页识别未完成，原有区域已保留，请重试或人工核对。` : "",
          ...remoteNotices, pending ? `另有 ${pending} 个区域尚未自动对应，可重新识别或修改。` : "",
          missing ? `还有 ${missing} 道题未对应答题区域。` : ""].filter(Boolean).join(" ");
        if (notice) { setSuggestionError(notice); setSuggestionTone(failures.length || pending || missing || retainLocalFillPages.length ? "warning" : "info"); }
        if (failures.length || retainLocalFillPages.length) message.warning(notice);
        else message.success(`识别已完成，已自动对应 ${matched.size} 道题；${missing ? `还有 ${missing} 道题未对应，` : ""}发现错误可直接修改`);
        return;
      }
      const covered = new Map<number, LayoutRegion[]>(latestRegions.current ?? []);
      for (const [pageNo, regions] of imageSuggestions) covered.set(pageNo, [...(covered.get(pageNo) ?? []), ...regions]);
      for (const [pageNo, regions] of identitySuggestions) covered.set(pageNo, [...(covered.get(pageNo) ?? []), ...regions]);
      // 重试保留失败页的大框；即使其内部已有部分人工题区，也须由网格应用或明确丢弃结束核对。
      const objective = mergeUnassignedFrames(objectiveRef.current, objectiveSuggestions, new Map());
      objectiveRef.current = objective;
      setObjectiveGroups(objective);
      for (const [pageNo, groups] of objective) covered.set(pageNo, [...(covered.get(pageNo) ?? []), ...groups]);
      const candidates = mergeUnassignedFrames(candidateRef.current, unassigned, covered);
      candidateRef.current = candidates;
      setUnassignedRegions(candidates);
      const candidateCount = [...candidates.values()].reduce((sum, regions) => sum + regions.length, 0);
      const remoteNotice = [remoteFailedPages.length ? `第 ${remoteFailedPages.join("、")} 页多模态后台分析未完成，已保留未关联的几何框；未使用本地 OCR 自动关联，请重试或人工核对。` : "", ...remoteNotices].filter(Boolean).join(" ") || undefined;
      if (!anchors.size && !imageSuggestions.size && !identitySuggestions.size && !objectiveSuggestions.size) {
        const notice = remoteNotice ?? (sourceKey ? failedPages.length
          ? `第 ${failedPages.join("、")} 页识别未完成；已保留 ${candidateCount} 个待关联框，可先关联题目或重试。`
          : candidateCount ? undefined
          : detectedFrames ? "已检查全部页面，识别到的答题框已有配置；原有区域已保留。"
          : "已检查全部页面，暂未检测到可靠的闭合答题框；可先手动补充区域，或换用更清晰的图片重试。"
          : "没有找到与本场题目相符的题号。请在左侧选择题目，再在答题卡上拖拽框选区域。");
        // 未关联框已显示在画布中，无需重复横幅；页面读取或识别失败仍提示。
        if (notice) {
          setSuggestionError(notice);
          setSuggestionTone(failedPages.length || remoteNotice ? "warning" : "info");
          message.warning(notice);
        }
        return;
      }
      const pages = new Map<number, LayoutRegion[]>(imageSuggestions);
      for (const [pageNo, pageAnchors] of anchors) {
        pages.set(pageNo, pageAnchors.map(({ question, top }, index) => {
          const y = clamp(top - .01, .02, .94);
          return {
            id: crypto.randomUUID(), question_id: question.id, label: question.question_no,
            x: .04, y: roundCoordinate(y), width: .92,
            height: roundCoordinate(clamp((pageAnchors[index + 1]?.top ?? .96) - y - .012, .04, .42)),
            option_regions: [], suggestion_confidence: .72,
            suggestion_source: "pdf_text_anchor" as const
          };
        }));
      }
      if (!isCurrent()) return;
      // 异步处理期间可能由外部刷新或恢复草稿补入人工区域，只填仍未配置的题。
      for (const [pageNo, regions] of pages) pages.set(pageNo,
        regions.filter(region => !latestCovered.current?.has(region.question_id)));
      if (pages.size) dispatchLayout({ type: "suggestRegions", pages, preserveExisting: true });
      // 身份建议进入独立集合；不能因本页没有题号而丢掉姓名、学号，也不增加题目完成数。
      if (identitySuggestions.size) dispatchLayout({ type: "suggestIdentityRegions", pages: identitySuggestions });
      if (remoteNotice) {
        setSuggestionError(remoteNotice); setSuggestionTone("warning"); message.warning(remoteNotice);
      } else if (failedPages.length) {
        const notice = `第 ${failedPages.join("、")} 页识别未完成，其余可识别区域已处理；请重试，已配置区域会保留。`;
        setSuggestionError(notice);
        setSuggestionTone("warning");
        message.warning(notice);
      } else if (sourceKey && (unmatched.size || candidateCount)) {
        setSuggestionError(`已保留本次可识别结果${candidateCount ? `，另有 ${candidateCount} 个答题框待关联题目` : ""}；可继续补充，不必一次全部匹配。`);
        setSuggestionTone("info");
      }
      const unmatchedNotice = unmatched.size ? `，另有 ${unmatched.size} 道题未识别` : "";
      const count = [...pages.values()].reduce((sum, regions) => sum + regions.length, 0);
      const identityCount = [...identitySuggestions.values()].reduce((sum, regions) => sum + regions.length, 0);
      const objectiveCount = [...objectiveSuggestions.values()].reduce((sum, regions) => sum + regions.length, 0);
      const modelNotice = remoteModels.size ? `多模态模型 ${[...remoteModels].join("、")} ` : "";
      message.success(`${modelNotice}已自动对应 ${count} 道题${identityCount ? `及 ${identityCount} 个身份信息区域` : ""}${objectiveCount ? `，另有 ${objectiveCount} 个选择题区域尚未定位到各题选项` : ""}；发现错误可直接修改${unmatchedNotice}`);
    } catch (error) {
      if (isCurrent()) {
        const notice = getUserErrorMessage(error, "自动识别未完成，请重试；已有区域已保留");
        setSuggestionError(notice);
        setSuggestionTone("error");
        message.error(notice);
      }
    } finally {
      await ocr?.terminate().catch(() => undefined);
      if (isCurrent()) {
        cancelOCR.current = undefined;
        busy.current = false;
        setSuggestingRegions(false);
      }
    }
  };

  return { suggestingRegions, suggestionProgress, suggestionError, suggestionModel, suggestionTone,
    unassignedRegions, objectiveGroups, fillGradingGroups, assignSuggestedRegion, assignSuggestedIdentity, discardSuggestedRegion, discardObjectiveGroup, suggestQuestionRegions };
}
