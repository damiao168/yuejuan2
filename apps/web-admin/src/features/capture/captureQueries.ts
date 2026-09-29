import { listStudents, type Student } from "../../api/org";
import {
  listAnswerSegments,
  listOcrTasks,
  listSubmissionPages,
  listSubmissions,
  type AnswerSegment,
  type OcrTask,
  type Submission,
  type SubmissionPage
} from "../../api/submissions";

export interface SubmissionView {
  submission: Submission;
  pages: SubmissionPage[];
  ocrTasks: OcrTask[];
  ocrNextCursor: string;
  ocrHasMore: boolean;
  segments: AnswerSegment[];
  detailError?: string;
}

export interface CapturePage {
  rows: SubmissionView[];
  students: Student[];
  studentLookupError?: unknown;
  nextCursor: string;
  hasMore: boolean;
}

export interface CaptureQueryDependencies {
  listSubmissions: typeof listSubmissions;
  listStudents: typeof listStudents;
  listSubmissionPages: typeof listSubmissionPages;
  listOcrTasks: typeof listOcrTasks;
  listAnswerSegments: typeof listAnswerSegments;
}

const productionDependencies: CaptureQueryDependencies = {
  listSubmissions,
  listStudents,
  listSubmissionPages,
  listOcrTasks,
  listAnswerSegments
};

export function compactSubmissionView(submission: Submission): SubmissionView {
  return { submission, pages: submission.pages ?? [], ocrTasks: [], ocrNextCursor: "", ocrHasMore: false, segments: [] };
}

export async function buildSubmissionView(
  submission: Submission,
  formatError: (error: unknown) => string,
  dependencies = productionDependencies
): Promise<SubmissionView> {
  // 各详情独立加载；单项失败仍展示已成功的数据，并保留错误提示，不能伪装成完整空结果。
  const [pagesResult, ocrResult, segmentResult] = await Promise.allSettled([
    dependencies.listSubmissionPages(submission.id),
    dependencies.listOcrTasks(submission.id, { limit: 20 }),
    dependencies.listAnswerSegments(submission.id)
  ]);
  const errors = [pagesResult, ocrResult, segmentResult]
    .filter((result): result is PromiseRejectedResult => result.status === "rejected")
    .map((result) => formatError(result.reason));
  return {
    submission,
    pages: pagesResult.status === "fulfilled" ? pagesResult.value.pages : submission.pages ?? [],
    ocrTasks: ocrResult.status === "fulfilled" ? ocrResult.value.tasks : [],
    ocrNextCursor: ocrResult.status === "fulfilled" ? ocrResult.value.next_cursor : "",
    ocrHasMore: ocrResult.status === "fulfilled" && ocrResult.value.has_more,
    segments: segmentResult.status === "fulfilled" ? segmentResult.value.segments : [],
    detailError: errors.length > 0 ? errors.join("；") : undefined
  };
}

export async function loadCapturePage(
  examId: string,
  canReadStudentNames: boolean,
  dependencies = productionDependencies
): Promise<CapturePage> {
  const result = await dependencies.listSubmissions(examId, { limit: 50 });
  const studentIDs = Array.from(new Set(result.submissions.map((item) => item.student_id).filter((id): id is string => Boolean(id))));
  let students: Student[] = [];
  let studentLookupError: unknown;
  // 只有可读姓名的视图才补查当前页学生，避免为匿名阅卷额外读取身份资料。
  if (canReadStudentNames && studentIDs.length > 0) {
    try {
      students = (await dependencies.listStudents({ ids: studentIDs, limit: 200 })).students;
    } catch (error) {
      studentLookupError = error;
    }
  }
  return {
    rows: result.submissions.map(compactSubmissionView),
    students,
    studentLookupError,
    nextCursor: result.next_cursor ?? "",
    hasMore: Boolean(result.has_more)
  };
}

export async function loadNextCapturePage(
  examId: string,
  cursor: string,
  canReadStudentNames: boolean,
  dependencies = productionDependencies
): Promise<CapturePage> {
  const result = await dependencies.listSubmissions(examId, { limit: 50, cursor });
  const studentIDs = Array.from(new Set(result.submissions.map((item) => item.student_id).filter((id): id is string => Boolean(id))));
  const students = canReadStudentNames && studentIDs.length > 0
    ? (await dependencies.listStudents({ ids: studentIDs, limit: 200 })).students
    : [];
  return {
    rows: result.submissions.map(compactSubmissionView),
    students,
    nextCursor: result.next_cursor ?? "",
    hasMore: Boolean(result.has_more)
  };
}
