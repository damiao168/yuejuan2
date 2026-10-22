import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { appealDraftKey, readAppealDraft, studentDraftOwner } from "./appealDrafts";
import { deferredResponse, installApi, jsonResponse, makeAppeal, makeQuestion, makeResult, resultRoute, studentUser } from "./test/studentAppBatch8Fixtures";

const changed = () => jsonResponse({ error: { code: "student_release_changed" } }, 409);
const failed = () => jsonResponse({ error: { code: "temporary_failure" } }, 503);
const explanation = "请核对第二步已经写出的完整计算过程。";
const confirmation = "本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。";
const recovery = "查看当前成绩并重新核对";
const draftKey = appealDraftKey(studentDraftOwner(studentUser), "exam-1", "release-7", "question-1");
const isAppeal = (path: string) => path.endsWith("/question-appeals");
const isAnnotation = (path: string) => path.endsWith("/questions/question-1/annotations");

beforeEach(() => {
  window.history.replaceState(null, "", "/#/exams/exam-1");
  window.sessionStorage.clear();
  vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
});
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

function installReceiptScenario() {
  const annotation = deferredResponse();
  let fulfill: (response: Response) => void = () => { throw new Error("Uninitialized receipt"); };
  let reject: (reason: unknown) => void = () => { throw new Error("Uninitialized receipt"); };
  const receipt = new Promise<Response>((resolve, fail) => { fulfill = resolve; reject = fail; });
  let annotationReads = 0;
  let records: "empty" | "accepted" | "failed" = "empty";
  let result = makeResult([makeQuestion(), makeQuestion({ question_id: "question-2", question_no: "2", feedback: "第二题当前评分依据" })]);
  let loginUser = studentUser;
  const calls = installApi((call) => {
    if (call.url.pathname === "/api/v1/auth/login") return jsonResponse({ user: loginUser });
    if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt;
    if (isAppeal(call.url.pathname)) return records === "failed" ? failed() : jsonResponse({ appeals: records === "accepted" ? [makeAppeal()] : [] });
    if (isAnnotation(call.url.pathname)) return ++annotationReads === 1 ? failed() : annotationReads === 2 ? annotation.promise : jsonResponse({ annotations: [] });
    return resultRoute(call, result);
  });
  return {
    calls, annotation,
    resolveReceipt: fulfill,
    rejectReceipt: reject,
    setRecords: (value: typeof records) => { records = value; },
    setResult: (value: typeof result) => { result = value; },
    setLoginUser: (value: typeof loginUser) => { loginUser = value; }
  };
}

async function prepareAppeal(selectRegion = false) {
  fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
  fireEvent.change(await screen.findByRole("textbox", { name: "具体说明" }), { target: { value: explanation } });
  if (selectRegion) {
    fireEvent.load(screen.getByAltText("本题答题区域"));
    fireEvent.click(screen.getByRole("button", { name: "选择整个答题区域" }));
  }
  const submit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
  await waitFor(() => expect(submit.disabled).toBe(false));
  return submit;
}

async function startWithAnnotationRetry(selectRegion = false) {
  const submit = await prepareAppeal(selectRegion);
  fireEvent.click(screen.getByRole("button", { name: "重试批注" }));
  fireEvent.click(submit);
}

function assertEvidenceHidden() {
  expect(document.querySelector(".question-drilldown .answer-review")).toBeNull();
  expect(screen.queryByText("第一题公开评分依据")).toBeNull();
  expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
  expect(screen.queryByAltText("本题答题区域")).toBeNull();
  expect(screen.queryByRole("button", { name: "确认提交（仅一次）" })).toBeNull();
}

function receiptRegion() {
  return screen.getByRole("region", { name: "第 1 题复核申请回执" });
}

function installSessionRecoveryScenario() {
  const oldRestore = deferredResponse();
  const currentRestore = deferredResponse();
  const exitReply = deferredResponse();
  const receipt = deferredResponse();
  const nextUser = { ...studentUser, id: "student-user-2", username: "student-002", display_name: "另一位学生" };
  let restoreReads = 0;
  const calls = installApi((call) => {
    if (call.url.pathname === "/api/v1/auth/me") return ++restoreReads === 1 ? oldRestore.promise : currentRestore.promise;
    if (call.url.pathname === "/api/v1/auth/logout") return exitReply.promise;
    if (call.url.pathname === "/api/v1/auth/login") return jsonResponse({ user: nextUser });
    if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt.promise;
    return resultRoute(call, makeResult());
  });
  return { oldRestore, currentRestore, exitReply, receipt, nextUser, calls };
}

describe("换版后的原版复核回执", () => {
  it.each(["accepted", "failed"] as const)("迟到批注409后保留成功确认，记录查询为%s也不能覆盖POST事实", async (recordState) => {
    const scenario = installReceiptScenario();
    render(<App />);
    await startWithAnnotationRetry();
    // 批注重试先开始，POST等待回执时才确认换版。证据必须立即隐藏，回执必须继续可见。
    await act(async () => scenario.annotation.resolve(changed()));
    assertEvidenceHidden();
    expect(within(receiptRegion()).getByText("正在提交第 7 版的复核申请，请等待回执。")).toBeTruthy();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: recovery }).disabled).toBe(true);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "退出登录" }).disabled).toBe(true);
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    scenario.setRecords(recordState);
    await act(async () => scenario.resolveReceipt(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText(confirmation);
    await screen.findByText("此申请对应第 7 版成绩，当前成绩需重新核对。");
    if (recordState === "failed") await screen.findByText(/申请记录暂时无法加载/);
    else await screen.findByRole("heading", { name: "我的复核申请" });
    expect(readAppealDraft(draftKey)).toBeUndefined();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: recovery }).disabled).toBe(false);
    expect(scenario.calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it("网络失败回执可见且原版含圈选草稿保留，明确核对新版后不自动迁移或重发", async () => {
    const scenario = installReceiptScenario();
    render(<App />);
    await startWithAnnotationRetry(true);
    const savedDraft = readAppealDraft(draftKey);
    await act(async () => scenario.annotation.resolve(changed()));
    await act(async () => scenario.rejectReceipt(new TypeError("Failed to fetch")));
    expect(within(receiptRegion()).getByRole("alert").textContent).toBe("暂时无法连接服务，请稍后重试。");
    expect(readAppealDraft(draftKey)).toEqual(savedDraft);
    expect(savedDraft?.selectedRegion?.width).toBe(1);
    assertEvidenceHidden();
    scenario.setResult(makeResult([makeQuestion({ feedback: "第八版当前评分依据", score: 18 })], { release_id: "release-8", release_version: 8 }));
    fireEvent.click(screen.getByRole("button", { name: recovery }));
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，18/ }));
    await screen.findByText("第八版当前评分依据");
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe("");
    expect(screen.queryByRole("region", { name: "第 1 题复核申请回执" })).toBeNull();
    expect(readAppealDraft(draftKey)).toEqual(savedDraft);
    expect(readAppealDraft(appealDraftKey(studentDraftOwner(studentUser), "exam-1", "release-8", "question-1"))).toBeUndefined();
    expect(scenario.calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it.each([
    ["appeal_window_closed", "本版成绩的复核窗口已截止，不能再提交；说明已保留，可联系学校管理员。"],
    ["appeal_source_release_invalid", "申请对应的成绩版本已不可用。请先查看当前成绩，草稿仍按原版本保留。"],
    ["student_release_changed", "学校已更新当前成绩。原版本草稿已保留，请查看当前成绩并重新核对题目后再申请复核。"]
  ])("迟到换版后POST返回%s仍显示原版失败，禁止在旧回执区重发", async (code, message) => {
    const scenario = installReceiptScenario();
    render(<App />);
    await startWithAnnotationRetry();
    await act(async () => scenario.annotation.resolve(changed()));
    await act(async () => scenario.resolveReceipt(jsonResponse({ error: { code } }, 409)));
    expect(within(receiptRegion()).getByRole("alert").textContent).toBe(message);
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    assertEvidenceHidden();
    expect(scenario.calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it("POST真实来源失效409独立关闭证据，显示失败和当前成绩核对入口", async () => {
    const receipt = deferredResponse();
    const calls = installApi((call) => call.init.method === "POST" ? receipt.promise : resultRoute(call, makeResult()));
    render(<App />);
    fireEvent.click(await prepareAppeal());
    await act(async () => receipt.resolve(jsonResponse({ error: { code: "appeal_source_release_invalid" } }, 409)));
    assertEvidenceHidden();
    expect(within(receiptRegion()).getByRole("alert").textContent).toContain("申请对应的成绩版本已不可用");
    expect(screen.getByRole<HTMLButtonElement>("button", { name: recovery }).disabled).toBe(false);
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    expect(calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it("提交中答题图失败触发的版本核对也不能卸载原版失败回执", async () => {
    const verification = deferredResponse();
    const receipt = deferredResponse();
    let questionReads = 0;
    const calls = installApi((call) => {
      if (call.init.method === "POST") return receipt.promise;
      if (call.url.pathname.endsWith("/questions/question-1") && ++questionReads > 1) return verification.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    fireEvent.click(await prepareAppeal());
    // 两个图片入口共用一次核对。核对409不能抹掉此前已经发出的POST及其失败结果。
    fireEvent.error(screen.getByAltText("第 1 题答题区域"));
    fireEvent.error(screen.getByAltText("本题答题区域"));
    expect(questionReads).toBe(2);
    await act(async () => verification.resolve(changed()));
    assertEvidenceHidden();
    expect(within(receiptRegion()).getByText("正在提交第 7 版的复核申请，请等待回执。")).toBeTruthy();
    await act(async () => receipt.resolve(failed()));
    expect(within(receiptRegion()).getByRole("alert").textContent).toBe("暂时无法连接服务，请稍后重试。");
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    expect(calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it("重复409不误报成功，记录查询失败可重查，核实同题同版本后才清草稿", async () => {
    const scenario = installReceiptScenario();
    render(<App />);
    await startWithAnnotationRetry();
    await act(async () => scenario.annotation.resolve(changed()));
    scenario.setRecords("failed");
    await act(async () => scenario.resolveReceipt(jsonResponse({ error: { code: "appeal_already_filed" } }, 409)));
    const region = receiptRegion();
    expect(within(region).getByText("本题在这一版成绩下已经提交过复核申请，请查看已有申请记录。")).toBeTruthy();
    await within(region).findByText(/无法确认是否已提交/);
    expect(screen.queryByText(confirmation)).toBeNull();
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    scenario.setRecords("accepted");
    fireEvent.click(within(region).getByRole("button", { name: "查看已有申请记录" }));
    await screen.findByText(confirmation);
    expect(readAppealDraft(draftKey)).toBeUndefined();
    expect(scenario.calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it.each(["same", "different"] as const)("换版后POST401仍可见，回执结束后%s账户重登遵守草稿隔离", async (identity) => {
    const scenario = installReceiptScenario();
    render(<App />);
    await startWithAnnotationRetry();
    await act(async () => scenario.annotation.resolve(changed()));
    await act(async () => scenario.resolveReceipt(jsonResponse({ error: { code: "unauthenticated" } }, 401)));
    const region = receiptRegion();
    expect(within(region).getByRole("alert").textContent).toContain("登录状态已失效");
    const login = within(region).getByRole<HTMLButtonElement>("button", { name: "重新登录" });
    expect(login.disabled).toBe(false);
    if (identity === "different") scenario.setLoginUser({ ...studentUser, id: "other-student", username: "student-002" });
    fireEvent.click(login);
    await screen.findByRole("heading", { name: "查看已发布成绩" });
    fireEvent.change(screen.getByRole("textbox", { name: "学校代码" }), { target: { value: "school-a" } });
    fireEvent.change(screen.getByRole("textbox", { name: "账号" }), { target: { value: identity === "same" ? "student-001" : "student-002" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "testing" } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    const reason = await screen.findByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" });
    expect(reason.value).toBe(identity === "same" ? explanation : "");
    expect(readAppealDraft(draftKey)?.reason).toBe(identity === "same" ? explanation : undefined);
    expect(screen.queryByRole("region", { name: "第 1 题复核申请回执" })).toBeNull();
    expect(scenario.calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it("成功POST先返回，迟到批注409隐藏证据但保留原版成功确认", async () => {
    const scenario = installReceiptScenario();
    render(<App />);
    await startWithAnnotationRetry();
    scenario.setRecords("failed");
    await act(async () => scenario.resolveReceipt(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText(confirmation);
    await act(async () => scenario.annotation.resolve(changed()));
    assertEvidenceHidden();
    expect(screen.getByText(confirmation)).toBeTruthy();
    expect(screen.getByText("此申请对应第 7 版成绩，当前成绩需重新核对。")).toBeTruthy();
    expect(readAppealDraft(draftKey)).toBeUndefined();
  });

  it.each(["hashchange", "popstate"] as const)("同一轮事件排队的收起、切题、切科和%s离开均被同步提交标记拦住", async (navigation) => {
    const receipt = deferredResponse();
    const result = makeResult([makeQuestion(), makeQuestion({ question_id: "question-2", question_no: "2" }), makeQuestion({ question_id: "question-3", question_no: "3", subject: "english" })]);
    const calls = installApi((call) => call.init.method === "POST" ? receipt.promise : resultRoute(call, result));
    render(<App />);
    const submit = await prepareAppeal();
    const form = submit.closest("form") as HTMLFormElement;
    const close = screen.getByRole("button", { name: "返回题目列表" });
    const first = screen.getByRole("button", { name: /^第 1 题，/ });
    const second = screen.getByRole("button", { name: /^第 2 题，/ });
    const english = screen.getByRole("button", { name: "英语" });
    // 同批事件可能仍拿到提交前的 state；这些处理器必须读 ref，不能只靠按钮禁用。
    act(() => {
      fireEvent.submit(form);
      fireEvent.click(close);
      fireEvent.click(first);
      fireEvent.click(second);
      fireEvent.click(english);
      window.history.replaceState(null, "", navigation === "hashchange" ? "/#/" : "/");
      window.dispatchEvent(new Event(navigation));
    });
    expect(screen.getByRole("heading", { name: "第 1 题 · 作答与评分依据" })).toBeTruthy();
    expect(window.location.pathname).toBe("/exams/exam-1");
    expect(window.location.hash).toBe("");
    expect(screen.getByRole("button", { name: "正在提交…" })).toBeTruthy();
    expect(calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
    await act(async () => receipt.resolve(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText(confirmation);
  });

  it.each(["switch", "close"] as const)("成功回执后%s题目，旧批注的迟到409不能污染新上下文", async (action) => {
    const scenario = installReceiptScenario();
    render(<App />);
    await startWithAnnotationRetry();
    await act(async () => scenario.resolveReceipt(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText(confirmation);
    if (action === "switch") {
      fireEvent.click(screen.getByRole("button", { name: /^第 2 题，/ }));
      await screen.findByText("第二题当前评分依据");
    } else fireEvent.click(screen.getByRole("button", { name: "返回题目列表" }));
    await act(async () => scenario.annotation.resolve(changed()));
    expect(screen.queryByText(confirmation)).toBeNull();
    expect(screen.queryByRole("button", { name: recovery })).toBeNull();
    expect(screen.queryByRole("region", { name: "第 1 题复核申请回执" })).toBeNull();
    if (action === "switch") expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe("");
    expect(scenario.calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it.each(["success", "http_failure", "network_failure"] as const)("退出先开始时关闭同批提交和等待窗，退出%s后再登录可以重新申请", async (outcome) => {
    const exitReply = deferredResponse();
    const receipt = deferredResponse();
    const exitRequest = exitReply.promise.then((response) => {
      if (outcome === "network_failure") throw new TypeError("Failed to fetch");
      return response;
    });
    const calls = installApi((call) => {
      if (call.url.pathname === "/api/v1/auth/logout") return exitRequest;
      if (call.url.pathname === "/api/v1/auth/login") return jsonResponse({ user: studentUser });
      if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    const submit = await prepareAppeal();
    const oldForm = submit.closest("form") as HTMLFormElement;
    const exit = screen.getByRole("button", { name: "退出登录" });
    // 故意在一个事件批次中保留旧表单并重复退出；同步 ref 必须在界面重绘前就禁止新POST。
    act(() => { fireEvent.click(exit); fireEvent.submit(oldForm); fireEvent.click(exit); });
    expect(screen.getByRole("status").textContent).toBe("正在退出登录…");
    expect(screen.queryByRole("textbox", { name: "具体说明" })).toBeNull();
    expect(screen.queryByRole("heading", { name: "查看已发布成绩" })).toBeNull();
    expect(readAppealDraft(draftKey)).toBeUndefined();
    expect(calls.filter((call) => call.url.pathname === "/api/v1/auth/logout")).toHaveLength(1);
    expect(calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(0);
    await act(async () => exitReply.resolve(outcome === "http_failure" ? failed() : jsonResponse({})));
    await screen.findByRole("heading", { name: "查看已发布成绩" });
    expect(screen.queryByRole("status")).toBeNull();
    fireEvent.change(screen.getByRole("textbox", { name: "学校代码" }), { target: { value: "school-a" } });
    fireEvent.change(screen.getByRole("textbox", { name: "账号" }), { target: { value: "student-001" } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "testing" } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
    const reason = await screen.findByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" });
    expect(reason.value).toBe("");
    fireEvent.change(reason, { target: { value: explanation } });
    const newSubmit = screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" });
    await waitFor(() => expect(newSubmit.disabled).toBe(false));
    fireEvent.click(newSubmit);
    await act(async () => receipt.resolve(jsonResponse({ appeal: makeAppeal() })));
    await screen.findByText(confirmation);
    expect(calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });

  it("先提交时同步拒绝退出，失败回执与草稿保留，回执结束后正常退出清草稿", async () => {
    const receipt = deferredResponse();
    const exitReply = deferredResponse();
    const calls = installApi((call) => {
      if (call.url.pathname === "/api/v1/auth/logout") return exitReply.promise;
      if (isAppeal(call.url.pathname) && call.init.method === "POST") return receipt.promise;
      return resultRoute(call, makeResult());
    });
    render(<App />);
    const submit = await prepareAppeal();
    const exit = screen.getByRole("button", { name: "退出登录" });
    act(() => { fireEvent.submit(submit.closest("form") as HTMLFormElement); fireEvent.click(exit); });
    expect(screen.getByRole("button", { name: "正在提交…" })).toBeTruthy();
    expect(calls.some((call) => call.url.pathname === "/api/v1/auth/logout")).toBe(false);
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    await act(async () => receipt.resolve(failed()));
    await screen.findByText("暂时无法连接服务，请稍后重试。");
    expect(readAppealDraft(draftKey)?.reason).toBe(explanation);
    fireEvent.click(screen.getByRole("button", { name: "退出登录" }));
    expect(screen.getByRole("status").textContent).toBe("正在退出登录…");
    expect(readAppealDraft(draftKey)).toBeUndefined();
    await act(async () => exitReply.resolve(jsonResponse({})));
    await screen.findByRole("heading", { name: "查看已发布成绩" });
    expect(calls.filter((call) => call.url.pathname === "/api/v1/auth/logout")).toHaveLength(1);
    expect(calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(1);
  });
});

describe("StrictMode会话恢复响应隔离", () => {
  it.each(["success", "failure"] as const)("过期恢复%s不能提前结束当前恢复，当前有效请求正常完成", async (outcome) => {
    const scenario = installSessionRecoveryScenario();
    render(<StrictMode><App /></StrictMode>);
    expect(scenario.calls.filter((call) => call.url.pathname === "/api/v1/auth/me")).toHaveLength(2);
    // StrictMode清理第一轮effect后发出第二轮；第一轮先返回，也不能修改第二轮的loading与账户。
    await act(async () => scenario.oldRestore.resolve(outcome === "success" ? jsonResponse({ user: studentUser }) : failed()));
    expect(screen.getByText("正在确认登录状态…")).toBeTruthy();
    expect(screen.queryByRole("heading", { name: "查看已发布成绩" })).toBeNull();
    await act(async () => scenario.currentRestore.resolve(outcome === "success" ? jsonResponse({ user: studentUser }) : failed()));
    if (outcome === "success") await screen.findByRole("button", { name: /^第 1 题，/ });
    else await screen.findByRole("heading", { name: "查看已发布成绩" });
    expect(screen.queryByText("正在确认登录状态…")).toBeNull();
  });

  it.each(["success", "failure"] as const)("退出完成后过期恢复%s不能恢复旧账户或离开登录终态", async (outcome) => {
    const scenario = installSessionRecoveryScenario();
    render(<StrictMode><App /></StrictMode>);
    await act(async () => scenario.currentRestore.resolve(jsonResponse({ user: studentUser })));
    await prepareAppeal();
    fireEvent.click(screen.getByRole("button", { name: "退出登录" }));
    expect(screen.getByRole("status").textContent).toBe("正在退出登录…");
    await act(async () => scenario.exitReply.resolve(jsonResponse({})));
    await screen.findByRole("heading", { name: "查看已发布成绩" });
    await act(async () => scenario.oldRestore.resolve(outcome === "success" ? jsonResponse({ user: studentUser }) : failed()));
    expect(screen.getByRole("heading", { name: "查看已发布成绩" })).toBeTruthy();
    expect(screen.queryByText("正在确认登录状态…")).toBeNull();
    expect(document.querySelector(".portal-shell")).toBeNull();
    expect(readAppealDraft(draftKey)).toBeUndefined();
    expect(scenario.calls.filter((call) => isAppeal(call.url.pathname) && call.init.method === "POST")).toHaveLength(0);
  });

  it.each(["success", "failure"] as const)("重新登录新账户后过期恢复%s不能替换账户、清草稿或强制登出", async (outcome) => {
    const scenario = installSessionRecoveryScenario();
    render(<StrictMode><App /></StrictMode>);
    await act(async () => scenario.currentRestore.resolve(jsonResponse({ user: studentUser })));
    fireEvent.click(await prepareAppeal());
    await act(async () => scenario.receipt.resolve(jsonResponse({ error: { code: "unauthenticated" } }, 401)));
    fireEvent.click(await screen.findByRole("button", { name: "重新登录" }));
    await screen.findByRole("heading", { name: "查看已发布成绩" });
    fireEvent.change(screen.getByRole("textbox", { name: "学校代码" }), { target: { value: "school-a" } });
    fireEvent.change(screen.getByRole("textbox", { name: "账号" }), { target: { value: scenario.nextUser.username } });
    fireEvent.change(screen.getByLabelText("密码"), { target: { value: "testing" } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    const nextQuestion = await screen.findByRole<HTMLButtonElement>("button", { name: /^第 1 题，/ });
    // 新账户可先显示题目列表，旧提交收尾仍可能短暂保持禁用；等待真实可操作状态再验证身份隔离。
    await waitFor(() => expect(nextQuestion.disabled).toBe(false));
    fireEvent.click(nextQuestion);
    const reason = await screen.findByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" });
    expect(reason.value).toBe("");
    fireEvent.change(reason, { target: { value: "请核对新账户本题的第二步计算过程。" } });
    const newKey = appealDraftKey(studentDraftOwner(scenario.nextUser), "exam-1", "release-7", "question-1");
    const saved = readAppealDraft(newKey);
    await act(async () => scenario.oldRestore.resolve(outcome === "success" ? jsonResponse({ user: studentUser }) : failed()));
    expect(screen.getByText(scenario.nextUser.display_name)).toBeTruthy();
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(saved?.reason);
    expect(readAppealDraft(newKey)).toEqual(saved);
    expect(readAppealDraft(draftKey)).toBeUndefined();
    expect(screen.queryByRole("heading", { name: "查看已发布成绩" })).toBeNull();
  });

  it("真正卸载后的有效恢复也不能清掉另一个App新账户的草稿", async () => {
    const scenario = installSessionRecoveryScenario();
    const oldView = render(<StrictMode><App /></StrictMode>);
    oldView.unmount();
    const newKey = appealDraftKey(studentDraftOwner(scenario.nextUser), "exam-1", "release-7", "question-1");
    installApi((call) => resultRoute(call, makeResult()), scenario.nextUser);
    render(<App />);
    await prepareAppeal();
    const saved = readAppealDraft(newKey);
    // 账户接受会清理其他身份的草稿；即使组件已卸载，过期恢复也不能再执行这项全局存储副作用。
    await act(async () => {
      scenario.currentRestore.resolve(jsonResponse({ user: studentUser }));
      scenario.oldRestore.resolve(jsonResponse({ user: studentUser }));
    });
    expect(screen.getByText(scenario.nextUser.display_name)).toBeTruthy();
    expect(readAppealDraft(newKey)).toEqual(saved);
    expect(screen.getByRole<HTMLTextAreaElement>("textbox", { name: "具体说明" }).value).toBe(explanation);
  });
});
