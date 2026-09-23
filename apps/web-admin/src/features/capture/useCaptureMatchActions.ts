import { App } from "antd";
import type { Dispatch, SetStateAction } from "react";
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
  async function matchStudent(
    submissionId: string,
    studentId: string,
    revision: number,
  ) {
    setActioning(true);
    try {
      await confirmStudentMatch(submissionId, studentId, revision);
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      message.success("学生匹配已确认");
    } catch (currentError) {
      message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      setActioning(false);
    }
  }

  async function markUnknown(submissionId: string, revision: number) {
    setActioning(true);
    try {
      await markStudentUnknown(
        submissionId,
        revision,
        "答卷上无法可靠识别学生身份",
      );
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      message.warning("已标记为未知答卷");
    } catch (currentError) {
      message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      setActioning(false);
    }
  }

  async function matchPage(page: CapturePage, pageNo: number) {
    setActioning(true);
    try {
      await confirmPageMatch(page.id, pageNo, page.revision);
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      message.success(`第 ${pageNo} 页页码已保存`);
    } catch (currentError) {
      message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      setActioning(false);
    }
  }

  async function changePageLifecycle(page: CapturePage) {
    setActioning(true);
    try {
      if (page.status === "deleted")
        await restoreCapturePage(page.id, page.revision, "恢复误删页面");
      else await deleteCapturePage(page.id, page.revision, "移除非答卷页面");
      await Promise.all([
        loadMatching(selectedId),
        loadDetail(selectedId, true),
      ]);
      message.success(page.status === "deleted" ? "页面已恢复" : "页面已删除");
    } catch (currentError) {
      message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      setActioning(false);
    }
  }
  async function splitSubmission(submissionId: string, pageId: string) {
    setActioning(true);
    try {
      setMatching(
        await splitCaptureSubmission(
          selectedId,
          submissionId,
          [pageId],
          "人工拆分混扫答卷",
        ),
      );
      await loadDetail(selectedId, true);
      message.success("已拆分为新答卷");
    } catch (currentError) {
      message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      setActioning(false);
    }
  }
  async function mergeSubmissions(targetId: string, sourceId: string) {
    setActioning(true);
    try {
      setMatching(
        await mergeCaptureSubmissions(
          selectedId,
          targetId,
          sourceId,
          "人工合并散页答卷",
        ),
      );
      await loadDetail(selectedId, true);
      message.success("答卷已合并");
    } catch (currentError) {
      message.error(getUserErrorMessage(currentError, "操作失败，请重试"));
    } finally {
      setActioning(false);
    }
  }

  return { matchStudent, markUnknown, matchPage, changePageLifecycle, splitSubmission, mergeSubmissions };
}
