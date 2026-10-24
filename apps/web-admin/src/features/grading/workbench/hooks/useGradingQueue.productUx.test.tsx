// @vitest-environment jsdom
import { act, useEffect } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewTask } from "../../../../api/review";
import { useGradingQueue } from "./useGradingQueue";

const api = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), graders: vi.fn(), info: vi.fn() }));
vi.mock("../../../../api/review", () => ({ listReviewTasks: api.list, getReviewTask: api.get }));
vi.mock("../../../../api/users", () => ({ listAllActiveGraders: api.graders }));
function task(id: string, question = "q1") { return { id, exam_id: "exam", exam_name: "期中", subject: "math", question_id: question, question_no: question, status: "pending", source: "manual_sample", anonymous_code: id } as ReviewTask; }
const aggregate = (total: number) => ({ total_count: total, remaining_count: total, completed_count: 0, status_counts: { pending: total }, reviewers: [] });
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => { resolve = resolvePromise; reject = rejectPromise; });
  return { promise, resolve, reject };
}
type TaskPage = { tasks: ReviewTask[]; has_more: boolean; next_cursor?: string; aggregate?: ReturnType<typeof aggregate> };

describe("task query and complete selection", () => {
  let root: Root; let queue: ReturnType<typeof useGradingQueue>;
  beforeEach(() => {
    vi.clearAllMocks(); vi.useFakeTimers(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true }); window.location.hash = "";
    api.graders.mockResolvedValue([]); root = createRoot(document.createElement("div"));
    api.list.mockImplementation(async (filter) => ({ tasks: filter.limit === 1 ? [] : [task("a"), task("b", "q2")], aggregate: aggregate(filter.unassigned ? 1 : 2), has_more: false }));
  });
  afterEach(async () => { await act(async () => root.unmount()); vi.useRealTimers(); });
  async function mount(canManageTasks = true, heldGroupTask?: { current: string }) {
    function Probe() {
      const currentQueue = useGradingQueue({ canManageTasks, canWork: false, currentUserId: "admin", initialExamId: "", personalScope: false, onInfo: api.info, heldGroupTask });
      queue = currentQueue;
      const { loadTasks } = currentQueue;
      useEffect(() => { void loadTasks(); }, [loadTasks]);
      return null;
    }
    await act(async () => root.render(<Probe />));
  }
  async function search(value: string) {
    await act(async () => { queue.setKeyword(value); });
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
  }
  it("整区当前题提交后保留共享图选择，防止自动换题丢掉其它行草稿", async () => {
    const held = { current: "a" };
    await mount(true, held);
    expect(queue.selectedTaskId).toBe("a");
    await act(async () => { queue.setTasks(current => current.map(item => item.id === "a" ? { ...item, status: "submitted" } : item)); });
    expect(queue.filteredTasks.some(item => item.id === "a")).toBe(false);
    expect(queue.selectedTaskId).toBe("a");
    await act(async () => { held.current = ""; queue.setTasks(current => [...current]); });
    expect(queue.selectedTaskId).toBe("b");
  });
  it("sends subject, question and unassigned filters to list and matched aggregate, keeping whole-state progress", async () => {
    await mount(); await act(async () => { queue.setSubjectFilter("math"); queue.setQuestionFilter("q1"); queue.setUnassignedOnly(true); });
    expect(api.list).toHaveBeenCalledWith(expect.objectContaining({ subject: "math", question_id: "q1", unassigned: true, status: "active", limit: 50 }));
    expect(api.list).toHaveBeenCalledWith(expect.objectContaining({ subject: "math", question_id: "q1", limit: 1 }));
    expect(queue.taskAggregate?.total_count).toBe(2); expect(queue.matchedAggregate?.total_count).toBe(1);
    expect(queue.subjectOptions).toEqual([{ value: "math", label: "数学" }]);
  });
  it("按学科代码固定排序，去重并忽略空值，不按中文标签或数字样式改变代码顺序", async () => {
    const subjects = ["math", "z", "_", "A", "a", "10", "2", "english", "Z", "math", "", undefined];
    const listed = subjects.map((subject, index) => ({ ...task(`subject-${index}`), subject }));
    api.list.mockImplementation(async (filter) => ({ tasks: filter.limit === 1 ? [] : listed, aggregate: aggregate(listed.length), has_more: false }));
    await mount();
    expect(queue.subjectOptions.map((option) => option.value)).toEqual(["10", "2", "A", "Z", "_", "a", "english", "math", "z"]);
    expect(queue.subjectOptions.find((option) => option.value === "english")?.label).toBe("英语");
    expect(queue.subjectOptions.find((option) => option.value === "math")?.label).toBe("数学");
    expect(queue.tasks.map((item) => item.subject)).toEqual(subjects);
  });
  it("does not select a partial result when fetching all matching pages fails", async () => {
    await mount();
    await act(async () => { queue.setAssignmentTaskIds(["a"]); });
    api.list.mockImplementation(async (filter) => {
      if (filter.cursor) throw new Error("second page unavailable");
      return { tasks: [task("only-first-page")], aggregate: aggregate(2), has_more: true, next_cursor: "next" };
    });
    await act(async () => { await queue.selectAllMatchingAssignments(); });
    expect(queue.assignmentTaskIds).toEqual(["a"]); expect(queue.tasks.map((item) => item.id)).toEqual(["a", "b"]);
    expect(api.info).toHaveBeenCalled();
  });
  it("selects complete matching tasks and excludes fixed double-mark assignments", async () => {
    await mount();
    api.list.mockImplementation(async (filter) => filter.cursor ? { tasks: [{ ...task("fixed"), source: "double_mark_required" }, task("last")], aggregate: aggregate(3), has_more: false } : { tasks: [task("first")], aggregate: aggregate(3), has_more: true, next_cursor: "next" });
    await act(async () => { await queue.selectAllMatchingAssignments(); });
    expect(queue.assignmentTaskIds).toEqual(["first", "last"]); expect(queue.hasMoreTasks).toBe(false);
  });
  it("视觉模型不可用后，第14至19题的人工接管任务可见且可全选，已开始或结束的任务仍不可改派", async () => {
    // 评分运行被模型配置阻塞后，服务端恢复的是 pending 人工任务；故障原因不能再挡住安排教师。
    const recovered = Array.from({ length: 6 }, (_, index) => ({
      ...task(`recovered-${index + 14}`, `q${index + 14}`), question_no: String(index + 14),
      source: index % 2 ? "rule_review_required" : "subjective_default_review", reason_code: "visual_model_unavailable"
    }));
    const unavailable = ["in_progress", "submitted", "completed", "cancelled"].map(status => ({ ...recovered[0], id: status, status }));
    const listed = [...recovered, ...unavailable, { ...recovered[0], id: "fixed-double-mark", source: "double_mark_required" }];
    api.list.mockImplementation(async (filter) => ({ tasks: filter.limit === 1 ? [] : listed, aggregate: aggregate(listed.length), has_more: false }));
    await mount();
    expect(queue.filteredTasks.filter(item => item.status === "pending" && item.source !== "double_mark_required").map(item => item.question_no)).toEqual(["14", "15", "16", "17", "18", "19"]);
    await act(async () => { queue.setTaskFilter("all"); });
    expect(queue.filteredTasks).toHaveLength(listed.length);
    expect(queue.assignableTasks.map(item => item.id)).toEqual(recovered.map(item => item.id));
    await act(async () => { await queue.selectAllMatchingAssignments(); });
    expect(queue.assignmentTaskIds).toEqual(recovered.map(item => item.id));
    expect(api.info).not.toHaveBeenCalled();
  });
  it("keeps cross-field search results visible and selectable with the same matched count", async () => {
    await mount();
    await act(async () => { queue.setKeyword("期中 math"); });
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    expect(api.list).toHaveBeenCalledWith(expect.objectContaining({ q: "期中 math", status: "active", limit: 50 }));
    expect(queue.filteredTasks.map((item) => item.id)).toEqual(["a", "b"]);
    expect(queue.matchedAggregate?.total_count).toBe(2);
    await act(async () => { await queue.selectAllMatchingAssignments(); });
    expect(queue.assignmentTaskIds).toEqual(["a", "b"]);
    expect(api.list).toHaveBeenCalledWith(expect.objectContaining({ q: "期中 math", status: "active", limit: 200 }));
  });
  it("blocks same-event select-all after a keyword change until the current search is loaded", async () => {
    api.list.mockImplementation(async (filter) => ({ tasks: filter.limit === 1 ? [] : [task(filter.q || "Alpha")], aggregate: aggregate(1), has_more: false }));
    await mount(); await search("Alpha");
    api.list.mockClear();
    // 同一事件中 state 尚未重绘；ref 必须先阻止旧 Alpha 回调继续请求。
    await act(async () => { queue.setKeyword("  Beta  "); await queue.selectAllMatchingAssignments(); });
    expect(queue.loadingTasks).toBe(true); expect(queue.assignmentTaskIds).toEqual([]);
    expect(api.list).not.toHaveBeenCalled(); expect(api.info).toHaveBeenCalledWith(expect.stringContaining("当前筛选"));
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    await act(async () => { await queue.selectAllMatchingAssignments(); });
    expect(api.list).toHaveBeenCalledWith(expect.objectContaining({ q: "Beta", limit: 200 }));
    expect(queue.assignmentTaskIds).toEqual(["Beta"]);
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    expect(queue.assignmentTaskIds).toEqual(["Beta"]);
  });
  it("discards an old select-all before the new keyword debounce finishes", async () => {
    const old = deferred<TaskPage>();
    await mount(); await search("Alpha");
    api.list.mockImplementation((filter) => filter.limit === 200 ? old.promise : Promise.resolve({ tasks: filter.limit === 1 ? [] : [task("Beta")], aggregate: aggregate(1), has_more: false }));
    let selecting!: Promise<void>;
    await act(async () => { selecting = queue.selectAllMatchingAssignments(); });
    await act(async () => { queue.setKeyword("Beta"); });
    expect(queue.selectingAll).toBe(false);
    await act(async () => { old.resolve({ tasks: [task("Alpha")], has_more: false }); await selecting; });
    expect(queue.assignmentTaskIds).toEqual([]); expect(queue.tasks.map((item) => item.id)).toEqual(["a", "b"]);
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    expect(queue.tasks.map((item) => item.id)).toEqual(["Beta"]);
  });
  it("does not let an old selection response unlock or replace a newer subject selection", async () => {
    const old = deferred<TaskPage>(); const latest = deferred<TaskPage>();
    await mount();
    api.list.mockImplementation((filter) => filter.limit === 200 ? (filter.subject ? latest.promise : old.promise) : Promise.resolve({ tasks: filter.limit === 1 ? [] : [task("current")], aggregate: aggregate(1), has_more: false }));
    let oldSelecting!: Promise<void>; let newSelecting!: Promise<void>;
    await act(async () => { oldSelecting = queue.selectAllMatchingAssignments(); });
    await act(async () => { queue.setSubjectFilter("math"); });
    await act(async () => { newSelecting = queue.selectAllMatchingAssignments(); });
    expect(queue.selectingAll).toBe(true);
    await act(async () => { old.resolve({ tasks: [task("stale")], has_more: false }); await oldSelecting; });
    expect(queue.selectingAll).toBe(true); expect(queue.assignmentTaskIds).toEqual([]);
    await act(async () => { latest.resolve({ tasks: [task("current")], has_more: false }); await newSelecting; });
    expect(queue.selectingAll).toBe(false); expect(queue.assignmentTaskIds).toEqual(["current"]);
  });
  it("ignores stale select-all errors after a question filter changes", async () => {
    const old = deferred<TaskPage>(); await mount();
    api.list.mockImplementation((filter) => filter.limit === 200 ? old.promise : Promise.resolve({ tasks: filter.limit === 1 ? [] : [task("new", "q2")], aggregate: aggregate(1), has_more: false }));
    let selecting!: Promise<void>; await act(async () => { selecting = queue.selectAllMatchingAssignments(); });
    await act(async () => { queue.setQuestionFilter("q2"); });
    await act(async () => { old.reject(new Error("old page failed")); await selecting; });
    expect(api.info).not.toHaveBeenCalled(); expect(queue.tasks.map((item) => item.id)).toEqual(["new"]); expect(queue.assignmentTaskIds).toEqual([]);
  });
  it.each(["exam", "status", "unassigned", "queue"] as const)("discards an in-flight selection after the %s filter changes", async (filter) => {
    const old = deferred<TaskPage>(); await mount();
    api.list.mockImplementation((query) => query.limit === 200 ? old.promise : Promise.resolve({ tasks: query.limit === 1 ? [] : [task("new")], aggregate: aggregate(1), has_more: false }));
    let selecting!: Promise<void>; await act(async () => { selecting = queue.selectAllMatchingAssignments(); });
    await act(async () => {
      if (filter === "exam") queue.setExamFilter("exam");
      if (filter === "status") queue.setTaskFilter("pending");
      if (filter === "unassigned") queue.setUnassignedOnly(true);
      if (filter === "queue") queue.setQueueScope("mine");
    });
    await act(async () => { old.resolve({ tasks: [task("stale")], has_more: false }); await selecting; });
    expect(queue.assignmentTaskIds).toEqual([]); expect(queue.tasks.map((item) => item.id)).toEqual(["new"]);
  });
  it("does not request all matching assignments for a teacher without task-management permission", async () => {
    await mount(false); api.list.mockClear();
    await act(async () => { await queue.selectAllMatchingAssignments(); });
    expect(api.list).not.toHaveBeenCalled(); expect(queue.assignmentTaskIds).toEqual([]);
  });
  it("does not select all after the current filtered list failed to load", async () => {
    await mount(); api.list.mockRejectedValue(new Error("current list unavailable"));
    await act(async () => { await queue.loadTasks(); }); api.list.mockClear();
    await act(async () => { await queue.selectAllMatchingAssignments(); });
    expect(api.list).not.toHaveBeenCalled(); expect(queue.assignmentTaskIds).toEqual([]); expect(api.info).toHaveBeenCalled();
  });
  it("keeps complete selection when an older automatic page finishes late", async () => {
    const more = deferred<TaskPage>();
    api.list.mockImplementation((filter) => filter.limit === 1 ? Promise.resolve({ tasks: [], aggregate: aggregate(2), has_more: false }) : filter.cursor ? more.promise : Promise.resolve({ tasks: [task("first")], aggregate: aggregate(2), has_more: true, next_cursor: "automatic-next" }));
    await mount(); expect(queue.loadingMoreTasks).toBe(true);
    api.list.mockImplementation(async (filter) => ({ tasks: filter.limit === 200 ? [task("first"), task("last")] : [], aggregate: aggregate(2), has_more: false }));
    await act(async () => { await queue.selectAllMatchingAssignments(); });
    await act(async () => { more.resolve({ tasks: [task("stale")], has_more: true, next_cursor: "stale-next" }); });
    expect(queue.tasks.map((item) => item.id)).toEqual(["first", "last"]); expect(queue.assignmentTaskIds).toEqual(["first", "last"]);
    expect(queue.hasMoreTasks).toBe(false); expect(queue.loadingMoreTasks).toBe(false);
  });
});
