import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

const studentUser = {
  id: "student-user-1",
  tenant_id: "tenant-1",
  tenant_code: "school-a",
  username: "student-001",
  display_name: "测试学生",
  roles: ["student"],
  permissions: ["student:grade:read"],
  data_scope: { student_id: "student-1" }
};

const releasedResult = {
  exam_id: "exam-1",
  release_id: "release-7",
  release_version: 7,
  total_score: 82,
  max_score: 100,
  questions: [{
    question_id: "question-1",
    question_no: "1",
    score: 14,
    max_score: 20
  }],
  appeal_window: {
    open: true,
    allowed_reason_codes: ["recognition_error", "rubric_disagreement"]
  }
};

interface ApiCall {
  url: URL;
  init: RequestInit;
}

type ApiHandler = (call: ApiCall) => Response | undefined | Promise<Response | undefined>;

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  window.location.hash = "/";
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" }
  });
}

function installApi(handler: ApiHandler) {
  fetchMock.mockImplementation(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const rawURL = typeof input === "string"
      ? input
      : input instanceof URL
        ? input.toString()
        : input.url;
    const call = { url: new URL(rawURL, "http://portal.test"), init };
    if (call.url.pathname === "/api/v1/auth/me") {
      return jsonResponse({ user: studentUser });
    }
    const response = await handler(call);
    if (response) return response;
    if (call.url.pathname === "/api/v1/student/question-appeals") {
      return jsonResponse({ appeals: [] });
    }
    throw new Error(`Unexpected API request: ${init.method ?? "GET"} ${call.url.pathname}`);
  });
}

function resultRoute(call: ApiCall, result: unknown = releasedResult) {
  if (call.url.pathname === "/api/v1/student/exams/exam-1/result") {
    return jsonResponse({ result });
  }
  return undefined;
}

async function renderReleasedResult(handler: ApiHandler = (call) => resultRoute(call)) {
  window.location.hash = "/exams/exam-1";
  installApi(handler);
  render(<App />);
  await screen.findByLabelText("总分");
}

describe("Student Portal released-score boundaries", () => {
  it("keeps the latest question and appeal target when an older response arrives late", async () => {
    const second = { ...releasedResult.questions[0], question_id: "question-2", question_no: "2", feedback: "第二题评分依据" };
    let resolveFirst!: (response: Response) => void;
    const firstResponse = new Promise<Response>((resolve) => { resolveFirst = resolve; });
    await renderReleasedResult((call) => {
      const result = resultRoute(call, { ...releasedResult, questions: [releasedResult.questions[0], second] });
      if (result) return result;
      if (call.url.pathname.endsWith("/annotations")) return jsonResponse({ annotations: [] });
      if (call.url.pathname.endsWith("/questions/question-1")) return firstResponse;
      if (call.url.pathname.endsWith("/questions/question-2")) return jsonResponse({ question: second });
      return undefined;
    });
    fireEvent.click(screen.getByRole("button", { name: /^第 1 题，/ }));
    fireEvent.click(screen.getByRole("button", { name: /^第 2 题，/ }));
    await screen.findByText("第二题评分依据");
    resolveFirst(jsonResponse({ question: { ...releasedResult.questions[0], feedback: "过期的第一题内容" } }));
    await waitFor(() => expect(screen.queryByText("正在加载本题…")).toBeNull());
    expect(screen.getByText("第二题评分依据")).toBeTruthy();
    expect(screen.queryByText("过期的第一题内容")).toBeNull();
    expect(screen.getByRole("heading", { name: "第 2 题 · 作答与评分依据" })).toBeTruthy();
  });

  it("does not show scores or grading details before a release exists", async () => {
    installApi((call) => {
      if (call.url.pathname === "/api/v1/student/exams") {
        return jsonResponse({ exams: [] });
      }
      return undefined;
    });

    render(<App />);

    await screen.findByText("学校尚未发布你的成绩");
    expect(screen.queryByText("总分")).toBeNull();
    expect(screen.queryByText("教师反馈")).toBeNull();
    expect(screen.queryByText("INTERNAL_AI_SCORE")).toBeNull();
  });

  it("shows the existing unreleased state for a direct result request", async () => {
    window.location.hash = "/exams/exam-1";
    installApi((call) => {
      if (call.url.pathname === "/api/v1/student/exams/exam-1/result") {
        return jsonResponse({ error: { code: "not_found", message: "raw release lookup failed" } }, 404);
      }
      return undefined;
    });

    render(<App />);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("学校尚未发布这场考试的成绩");
    expect(alert.textContent).not.toContain("raw release lookup failed");
    expect(screen.queryByText("总分")).toBeNull();
  });

  it("renders the immutable released snapshot instead of mutable grading values", async () => {
    const resultWithInternalLiveValues = {
      ...releasedResult,
      current_grading_total: 91,
      questions: [{
        ...releasedResult.questions[0],
        current_grading_score: 19
      }]
    };

    await renderReleasedResult((call) => resultRoute(call, resultWithInternalLiveValues));

    expect(screen.getByText("已发布成绩 · 第 7 版")).toBeTruthy();
    expect(screen.getByLabelText("总分").textContent).toContain("82");
    expect(screen.getByLabelText("总分").textContent).not.toContain("91");
    expect(screen.getByRole("button", { name: /14 \/ 20 分/ }).textContent).not.toContain("19");
  });

  it("renders subject balance radar and grouped bar charts from released aggregates", async () => {
    await renderReleasedResult((call) => resultRoute(call, {
      ...releasedResult,
      subject_balance: [
        { subject: "chinese", student_score_rate: .76, school_mean_score_rate: .72, sample_size: 80 },
        { subject: "math", student_score_rate: .82, school_mean_score_rate: .74, sample_size: 80 },
        { subject: "english", student_score_rate: .69, school_mean_score_rate: .71, sample_size: 80 },
        { subject: "physics", student_score_rate: .88, school_mean_score_rate: .77, sample_size: 80 }
      ]
    }));

    expect(screen.getByRole("heading", { name: "已公布科目的得分对比" })).toBeTruthy();
    expect(screen.queryByRole("img", { name: "我的各科得分率与本次参试学生平均雷达图" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "展开学科雷达图" }));
    expect(screen.getByRole("img", { name: "我的各科得分率与本次参试学生平均雷达图" })).toBeTruthy();
    expect(screen.getByRole("img", { name: "各科得分率分组柱状图" })).toBeTruthy();
    expect(screen.getByRole("button", { name: /我的得分率/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /本次参试学生平均/ })).toBeTruthy();
  });

  it("uses the shared legend to toggle both radar and bar chart series", async () => {
    await renderReleasedResult((call) => resultRoute(call, {
      ...releasedResult,
      subject_balance: [
        { subject: "chinese", student_score_rate: .76, school_mean_score_rate: .72, sample_size: 80 },
        { subject: "math", student_score_rate: .82, school_mean_score_rate: .74, sample_size: 80 },
        { subject: "english", student_score_rate: .69, school_mean_score_rate: .71, sample_size: 80 }
      ]
    }));

    fireEvent.click(screen.getByRole("button", { name: "展开学科雷达图" }));
    const radar = screen.getByRole("img", { name: "我的各科得分率与本次参试学生平均雷达图" });
    const bars = screen.getByRole("img", { name: "各科得分率分组柱状图" });
    expect(radar.querySelector(".radar-series.student")).toBeTruthy();
    expect(bars.querySelector(".subject-bar.student")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /我的得分率/ }));

    expect(radar.querySelector(".radar-series.student")).toBeNull();
    expect(bars.querySelector(".subject-bar.student")).toBeNull();
    expect(radar.querySelector(".radar-series.average")).toBeTruthy();
    expect(bars.querySelector(".subject-bar.average")).toBeTruthy();
  });

  it("classifies dense question rows against each released median score", async () => {
    await renderReleasedResult((call) => resultRoute(call, {
      ...releasedResult,
      exam: { name: "数学月考", subject: "math", exam_type: "monthly", published_at: "2026-08-30T00:00:00Z" },
      questions: [
        {
          question_id: "question-1", question_no: "1", score: 5, max_score: 10,
          correct_answer: "B", actual_answer: "B",
          cohort: { sample_size: 40, mean_score_rate: .5, full_score_rate: .1, zero_score_rate: .1, class_mean_score: 4.8, school_mean_score: 4.6, median_score: 5 }
        },
        {
          question_id: "question-2", question_no: "2", score: 4.5, max_score: 10,
          correct_answer: "AC", actual_answer: "A",
          cohort: { sample_size: 40, mean_score_rate: .6, full_score_rate: .2, zero_score_rate: .05, class_mean_score: 6.1, school_mean_score: 5.9, median_score: 5 }
        }
      ]
    }));

    expect(screen.getByRole("heading", { name: "数学逐题分析" })).toBeTruthy();
    expect(screen.getAllByRole("columnheader").map((cell) => cell.textContent)).toEqual([
      "题号", "正确答案", "实际答案", "得分", "班级平均分", "参试学生平均分", "群体中位分对比"
    ]);
    expect(screen.getByText("达到群体中位分")).toBeTruthy();
    expect(screen.getByText("低于群体中位分")).toBeTruthy();
    expect(screen.queryByText("已掌握")).toBeNull();
  });

  it("pages through annotated paper images and switches to the released high-score paper", async () => {
    await renderReleasedResult((call) => {
      const released = resultRoute(call, {
        ...releasedResult,
        questions: [
          { ...releasedResult.questions[0], page_no: 1, answer_geometry: { x: .1, y: .15, width: .35, height: .2 } },
          { question_id: "question-2", question_no: "2", score: 18, max_score: 20, page_no: 2, answer_geometry: { x: .2, y: .25, width: .4, height: .2 } }
        ],
        paper_pages: [
          { page_no: 1, question_id: "question-1", submission_page_id: "page-1" },
          { page_no: 2, question_id: "question-2", submission_page_id: "page-2" }
        ],
        high_score_paper: {
          available: true,
          total_score: 98,
          max_score: 100,
          pages: [
            { page_no: 1, question_id: "high-question-1", submission_page_id: "high-page-1" },
            { page_no: 2, question_id: "high-question-2", submission_page_id: "high-page-2" }
          ]
        }
      });
      if (released) return released;
      if (call.url.pathname.endsWith("/annotations")) {
        return jsonResponse({ annotations: [{
          id: "annotation-paper-1", answer_segment_id: "segment-1", submission_page_id: "page-1",
          type: "rectangle", geometry: { kind: "rectangle", x: .1, y: .15, width: .35, height: .2 },
          content: "步骤批注", created_at: "2026-08-30T00:00:00Z", updated_at: "2026-08-30T00:00:00Z"
        }] });
      }
      return undefined;
    });

    expect(screen.getByRole("img", { name: "本人试卷第 1 页" })).toBeTruthy();
    expect(await screen.findByTitle("步骤批注")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "下一页" }));
    expect(screen.getByRole("img", { name: "本人试卷第 2 页" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "查看高分试卷" }));
    const highScoreImage = screen.getByRole("img", { name: "高分试卷第 1 页" });
    expect(highScoreImage.getAttribute("src")).toContain("variant=high_score");
    expect(screen.getByRole("button", { name: "查看我的试卷" })).toBeTruthy();
  });

  it("renders public question feedback without exposing internal grading metadata", async () => {
    await renderReleasedResult((call) => {
      const released = resultRoute(call);
      if (released) return released;
      if (call.url.pathname === "/api/v1/student/exams/exam-1/questions/question-1") {
        return jsonResponse({
          question: {
            ...releasedResult.questions[0],
            feedback: "请复核第二步的计算过程。",
            rubric_summary: ["列式正确"],
            ai_internal_rationale: "INTERNAL_AI_RATIONALE",
            grader_identity: "INTERNAL_GRADER_IDENTITY",
            model_confidence: "INTERNAL_MODEL_CONFIDENCE",
            hidden_rubric_notes: "INTERNAL_HIDDEN_RUBRIC"
          }
        });
      }
      if (call.url.pathname.endsWith("/annotations")) {
        return jsonResponse({
          annotations: [{
            id: "annotation-1",
            answer_segment_id: "segment-1",
            submission_page_id: "page-1",
            type: "rectangle",
            geometry: { kind: "rectangle", x: 0.1, y: 0.1, width: 0.2, height: 0.2 },
            content: "公开批注内容",
            created_at: "2026-08-30T00:00:00Z",
            updated_at: "2026-08-30T00:00:00Z",
            private_note: "INTERNAL_PRIVATE_NOTE"
          }]
        });
      }
      return undefined;
    });

    fireEvent.click(screen.getByRole("button", { name: /14 \/ 20 分/ }));

    await screen.findByText("请复核第二步的计算过程。");
    expect(screen.getByText("公开批注内容")).toBeTruthy();
    for (const internalValue of [
      "INTERNAL_AI_RATIONALE",
      "INTERNAL_GRADER_IDENTITY",
      "INTERNAL_MODEL_CONFIDENCE",
      "INTERNAL_HIDDEN_RUBRIC",
      "INTERNAL_PRIVATE_NOTE"
    ]) {
      expect(screen.queryByText(internalValue)).toBeNull();
    }
  });

  it("submits an appeal with exam, release, question, reason, and student session context", async () => {
    let appealCall: ApiCall | undefined;
    await renderReleasedResult((call) => {
      const released = resultRoute(call);
      if (released) return released;
      if (call.url.pathname === "/api/v1/student/exams/exam-1/questions/question-1") {
        return jsonResponse({ question: releasedResult.questions[0] });
      }
      if (call.url.pathname.endsWith("/annotations")) {
        return jsonResponse({ annotations: [] });
      }
      if (call.url.pathname === "/api/v1/student/exams/exam-1/question-appeals" && call.init.method === "POST") {
        appealCall = call;
        return jsonResponse({ appeal: { id: "appeal-1", status: "submitted" } });
      }
      return undefined;
    });

    fireEvent.click(screen.getByRole("button", { name: /14 \/ 20 分/ }));
    const reason = await screen.findByRole("textbox", { name: "具体说明" });
    fireEvent.change(reason, { target: { value: "第二步计算过程已正确完成，请重新核对采分点。" } });
    // 表单先核对当前版本是否已有申请；文本框出现不代表核对已完成。
    await waitFor(() => expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认提交（仅一次）" }).disabled).toBe(false));
    fireEvent.click(screen.getByRole("button", { name: "确认提交（仅一次）" }));

    await waitFor(() => expect(appealCall).toBeTruthy());
    const payload = JSON.parse(String(appealCall?.init.body)) as Record<string, unknown>;
    expect(appealCall?.url.pathname).toBe("/api/v1/student/exams/exam-1/question-appeals");
    expect(appealCall?.init.credentials).toBe("include");
    expect(payload).toEqual({
      source_release_id: "release-7",
      question_id: "question-1",
      reason_code: "recognition_error",
      reason: "第二步计算过程已正确完成，请重新核对采分点。"
    });
    expect(payload.student_id).toBeUndefined();
  });

  it("does not offer a duplicate appeal for the same release and question", async () => {
    await renderReleasedResult((call) => {
      const released = resultRoute(call);
      if (released) return released;
      if (call.url.pathname === "/api/v1/student/exams/exam-1/questions/question-1") {
        return jsonResponse({ question: releasedResult.questions[0] });
      }
      if (call.url.pathname.endsWith("/annotations")) {
        return jsonResponse({ annotations: [] });
      }
      if (call.url.pathname === "/api/v1/student/question-appeals") {
        return jsonResponse({
          appeals: [{
            id: "appeal-existing",
            exam_id: "exam-1",
            source_release_id: "release-7",
            source_release_version: 7,
            question_id: "question-1",
            question_no: "1",
            source_score: 14,
            source_max_score: 20,
            reason_code: "recognition_error",
            reason: "已有复核说明",
            status: "under_review",
            created_at: "2026-08-30T00:00:00Z",
            updated_at: "2026-08-30T00:00:00Z"
          }]
        });
      }
      return undefined;
    });

    fireEvent.click(screen.getByRole("button", { name: /14 \/ 20 分/ }));

    await screen.findByText("本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。");
    expect(screen.queryByRole("button", { name: "确认提交（仅一次）" })).toBeNull();
  });

  it("shows a safe non-blank state when the result API fails", async () => {
    window.location.hash = "/exams/exam-1";
    installApi((call) => {
      if (call.url.pathname === "/api/v1/student/exams/exam-1/result") {
        throw new Error("postgresql password=SHOULD_NOT_LEAK");
      }
      return undefined;
    });

    render(<App />);

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("暂时无法连接服务，请稍后重试");
    expect(alert.textContent).not.toContain("postgresql");
    expect(alert.textContent).not.toContain("SHOULD_NOT_LEAK");
    expect(screen.getByRole("button", { name: "重试" })).toBeTruthy();
  });

  it("keeps the released question row visible when question detail is unavailable", async () => {
    await renderReleasedResult((call) => {
      const released = resultRoute(call);
      if (released) return released;
      if (call.url.pathname === "/api/v1/student/exams/exam-1/questions/question-1") {
        return jsonResponse({ error: { code: "not_found", message: "answer_segment_id=INTERNAL_SEGMENT" } }, 404);
      }
      return undefined;
    });

    const questionButton = screen.getByRole("button", { name: /14 \/ 20 分/ });
    fireEvent.click(questionButton);

    await screen.findByText(/本题详情暂不可用，考试总分仍可查看。/);
    expect(screen.getByRole("button", { name: /14 \/ 20 分/ })).toBeTruthy();
    expect(screen.queryByText(/INTERNAL_SEGMENT/)).toBeNull();
  });
});
