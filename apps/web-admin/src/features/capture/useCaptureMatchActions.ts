import { App } from "antd";
import { useEffect, useRef, type Dispatch, type SetStateAction } from "react";
import { confirmStudentMatch, markStudentUnknown, confirmPageMatch, deleteCapturePage, restoreCapturePage, splitCaptureSubmission, mergeCaptureSubmissions, type CapturePage, type MatchingQueue } from "../../api/capture";
import { getUserErrorMessage } from "../../api/client";

interface CaptureMatchActionsOptions {
  selectedId: string;
  loadMatching: (batchId: string) => Promise<void>;
  loadDetail: (batchId: string, quiet?: boolean) => Promise<void>;
  setMatching: Dispatch<SetStateAction<MatchingQueue | undefined>>;
  setActioning: Dispatch<SetStateAction<boolean>>;
}

export function useCaptureMatchActions({ selectedId, loadMatching, loadDetail, setMatching, setActioning }: CaptureMatchActionsOptions) {
  const { message } = App.useApp();
  const currentBatch = useRef(selectedId);
  currentBatch.current = selectedId;
  const operation = useRef(0);
  const inFlight = useRef<string | null>(null);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; operation.current += 1; }; }, []);
  useEffect(() => { operation.current += 1; inFlight.current = null; setActioning(false); }, [selectedId, setActioning]);
  function begin() {
    if (!mounted.current || currentBatch.current !== selectedId || inFlight.current === selectedId) return undefined;
    const request = ++operation.current;
    inFlight.current = selectedId;
    setActioning(true);
    // 写回队列和后续补读也属于发出时的批次；旧动作成功不应清空新批次已读材料。
    return () => mounted.current && currentBatch.current === selectedId && operation.current === request;
  }
  function finish(isCurrent: () => boolean) {
    if (isCurrent()) { inFlight.current = null; setActioning(false); }
  }
  async function matchStudent(
    submissionId: string,
    studentId: string,
    revision: number,
  ) {
    const isCurrent = begin();
    if (!isCurrent) return;
    try {
      await confirmStudentMatch(submissionId, studentId, revision);
      if (!isCurrent()) return;
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      if (isCurrent()) message.success("学生匹配已确认");
    } catch (currentError) {
      if (isCurrent()) message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      finish(isCurrent);
    }
  }

  async function markUnknown(submissionId: string, revision: number) {
    const isCurrent = begin();
    if (!isCurrent) return;
    try {
      await markStudentUnknown(
        submissionId,
        revision,
        "答卷上无法可靠识别学生身份",
      );
      if (!isCurrent()) return;
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      if (isCurrent()) message.warning("已标记为未知答卷");
    } catch (currentError) {
      if (isCurrent()) message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      finish(isCurrent);
    }
  }

  async function matchPage(page: CapturePage, pageNo: number) {
    const isCurrent = begin();
    if (!isCurrent) return;
    try {
      await confirmPageMatch(page.id, pageNo, page.revision);
      if (!isCurrent()) return;
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      if (isCurrent()) message.success(`第 ${pageNo} 页页码已保存`);
    } catch (currentError) {
      if (isCurrent()) message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      finish(isCurrent);
    }
  }

  async function changePageLifecycle(page: CapturePage) {
    const isCurrent = begin();
    if (!isCurrent) return;
    try {
      if (page.status === "deleted")
        await restoreCapturePage(page.id, page.revision, "恢复误删页面");
      else await deleteCapturePage(page.id, page.revision, "移除非答卷页面");
      if (!isCurrent()) return;
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      if (isCurrent()) message.success(page.status === "deleted" ? "页面已恢复" : "页面已删除");
    } catch (currentError) {
      if (isCurrent()) message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      finish(isCurrent);
    }
  }
  async function splitSubmission(submissionId: string, pageId: string) {
    const isCurrent = begin();
    if (!isCurrent) return;
    try {
      const queue = await splitCaptureSubmission(
          selectedId,
          submissionId,
          [pageId],
          "人工拆分混扫答卷",
        );
      if (!isCurrent()) return;
      setMatching(queue);
      await loadDetail(selectedId, true);
      if (isCurrent()) message.success("已拆分为新答卷");
    } catch (currentError) {
      if (isCurrent()) message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      finish(isCurrent);
    }
  }
  async function mergeSubmissions(targetId: string, sourceId: string) {
    const isCurrent = begin();
    if (!isCurrent) return;
    try {
      const queue = await mergeCaptureSubmissions(
          selectedId,
          targetId,
          sourceId,
          "人工合并散页答卷",
        );
      if (!isCurrent()) return;
      setMatching(queue);
      // 页面刷新失败不能把已保存的合并说成失败，返回的新队列仍可供面板核对目标。
      try {
        await loadDetail(selectedId, true);
        if (isCurrent()) message.success("答卷已合并");
      } catch (refreshError) {
        if (isCurrent()) message.warning(`答卷已合并；批次刷新失败，请刷新查看最新页面。${getUserErrorMessage(refreshError, "暂时无法读取最新批次")}`);
      }
    } catch (currentError) {
      if (isCurrent()) message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
      // 合并面板需要区分成功与失败，不能把已处理的失败当作成功完成。
      throw currentError;
    } finally {
      finish(isCurrent);
    }
  }

  return { matchStudent, markUnknown, matchPage, changePageLifecycle, splitSubmission, mergeSubmissions };
}
