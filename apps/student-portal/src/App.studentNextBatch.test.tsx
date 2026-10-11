import {
	act,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { PortalApiError } from "./api";
import { friendlyError } from "./errors";
import {
	appealDraftKey,
	listAppealDrafts,
	studentDraftOwner,
	writeAppealDraft,
} from "./appealDrafts";
import { ScoreHero } from "./features/results/ResultOverview";
import { SubjectPerformance } from "./features/results/SubjectPerformance";
import {
	installApi,
	jsonResponse,
	makeAppeal,
	makeQuestion,
	makeResult,
	resultRoute,
	studentUser,
} from "./test/studentAppBatch8Fixtures";

beforeEach(() => {
	window.location.hash = "/exams/exam-1";
	window.sessionStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("学生查分与复核第二批真实边界", () => {
	it("六科应考但只公布三科时合计不是完整考试总分，未公布分数不会补0", () => {
		const subjects = [
			"math",
			"chinese",
			"english",
			"physics",
			"chemistry",
			"biology",
		].map((subject, index) => ({
			exam_id: `exam-${index}`,
			subject,
			name: subject,
			status: index < 3 ? "published" : "unpublished",
			statistics_available: false,
			...(index < 3
				? { score: 10, max_score: 20, published_at: "2026-10-01T00:00:00Z" }
				: {}),
		}));
		render(
			<ScoreHero
				result={makeResult([], {
					exam: {
						name: "数学",
						subject: "mathematics",
						exam_type: "school_exam",
						published_at: "2026-10-01T00:00:00Z",
					},
					overall_total_score: 30,
					overall_max_score: 60,
					session_summary: { published_count: 3, expected_count: 6, subjects },
				})}
			/>,
		);
		expect(screen.getByText("已发布科目合计（3/6科）")).toBeTruthy();
		expect(screen.queryByText("本次考试总分")).toBeNull();
		expect(screen.getAllByText(/等待学校发布/)).toHaveLength(3);
		expect(screen.getByText("数学成绩")).toBeTruthy();
	});
	it("科目缺项及隐私原因仍可见，但不进入图形坐标", () => {
		const { container } = render(
			<SubjectPerformance
				items={[]}
				session={{
					published_count: 2,
					expected_count: 3,
					subjects: [
						{
							exam_id: "math",
							subject: "math",
							name: "数学",
							status: "published",
							score: 8,
							max_score: 10,
							statistics_available: true,
							mean_score_rate: 0.6,
							sample_size: 10,
						},
						{
							exam_id: "phy",
							subject: "physics",
							name: "物理",
							status: "published",
							score: 8,
							max_score: 10,
							statistics_available: false,
							statistics_reason: "small_cohort",
						},
						{
							exam_id: "bio",
							subject: "biology",
							name: "生物",
							status: "unpublished",
							statistics_available: false,
							statistics_reason: "unpublished",
						},
					],
				}}
			/>,
		);
		expect(screen.getByText("物理：参比人数不足，不展示")).toBeTruthy();
		expect(screen.getByText("生物：尚未发布")).toBeTruthy();
		expect(container.querySelectorAll(".subject-bar.average")).toHaveLength(1);
		expect(screen.queryByRole("button", { name: "展开学科雷达图" })).toBeNull();
	});
	it("未来窗口展示即将开始，不说未来日期已截止", async () => {
		installApi((call) =>
			resultRoute(
				call,
				makeResult([], {
					appeal_window: {
						open: false,
						state: "upcoming",
						opens_at: "2030-10-01T00:00:00Z",
						closes_at: "2030-10-02T00:00:00Z",
					},
				}),
			),
		);
		render(<App />);
		await screen.findByText(/复核将在/);
		expect(screen.queryByText(/已于.*结束/)).toBeNull();
		expect(screen.queryByRole("button", { name: "申请成绩复核" })).toBeNull();
	});
	it("前置复核入口定位公开题目，申请本题读取原发布并聚焦真实申请表", async () => {
		const calls = installApi((call) => resultRoute(call, makeResult()));
		render(<App />);
		const start = await screen.findByRole("button", { name: "申请成绩复核" });
		const review = document.getElementById("question-review")!;
		review.scrollIntoView = vi.fn();
		fireEvent.click(start);
		expect(review.scrollIntoView).toHaveBeenCalledWith({ behavior: "smooth" });
		expect(document.activeElement).toBe(document.querySelector("[data-question-id]"));
		fireEvent.click(screen.getByRole("button", { name: "申请本题复核" }));
		const input = await screen.findByRole("textbox", { name: "具体说明" });
		await waitFor(() => expect(document.activeElement).toBe(input));
		expect(calls.filter((call) => call.url.pathname.endsWith("questions/question-1"))[0]?.url.searchParams.get("expected_release_id")).toBe("release-7");
	});
	it("新版先提示且保留当前输入，显式切换后旧草稿可复制且圈选不迁移", async () => {
		let result = makeResult();
		const calls = installApi((call) => resultRoute(call, result));
		render(<App />);
		fireEvent.click(await screen.findByRole("button", { name: /^第 1 题，/ }));
		const input = await screen.findByRole<HTMLTextAreaElement>("textbox", {
			name: "具体说明",
		});
		fireEvent.change(input, {
			target: { value: "核对原卷中第二步的计算过程是否完整。" },
		});
		result = makeResult([makeQuestion({ score: 18 })], {
			release_id: "release-8",
			release_version: 8,
			total_score: 86,
		});
		await act(async () => window.dispatchEvent(new Event("focus")));
		await screen.findByRole("button", { name: "查看新版成绩" });
		expect(screen.getByRole("textbox", { name: "具体说明" })).toBe(input);
		expect(input.value).toContain("第二步");
		fireEvent.click(screen.getByRole("button", { name: "查看新版成绩" }));
		await screen.findByText(/已更新为第 8 版/);
		expect(
			screen.getByRole<HTMLTextAreaElement>("textbox", { name: "旧版复核说明" })
				.value,
		).toContain("第二步");
		fireEvent.click(screen.getByRole("button", { name: /^第 1 题，/ }));
		expect(
			(
				await screen.findByRole<HTMLTextAreaElement>("textbox", {
					name: "具体说明",
				})
			).value,
		).toBe("");
		const reads = calls.filter((call) =>
			call.url.pathname.endsWith("questions/question-1"),
		);
		expect(
			reads[reads.length - 1]?.url.searchParams.get("expected_release_id"),
		).toBe("release-8");
	});
	it("历史复核零分不由后续当前14分替代，并可重复打开当前本题", async () => {
		installApi((call) =>
			call.url.pathname.endsWith("question-appeals")
				? jsonResponse({
						appeals: [
							makeAppeal({
								status: "resolved",
								new_release_id: "release-8",
								new_release_version: 8,
								reviewed_score: 0,
								reviewed_max_score: 20,
							}),
						],
					})
				: resultRoute(call, makeResult()),
		);
		render(<App />);
		await screen.findByText(/复核后得分：0 \/ 20/);
		await screen.findByText("本题当前成绩：14");
		fireEvent.click(screen.getByRole("button", { name: "查看本题当前成绩" }));
		await screen.findByRole("heading", { name: "第 1 题 · 作答与评分依据" });
		fireEvent.click(screen.getByRole("button", { name: /^第 1 题，/ }));
		await waitFor(() =>
			expect(
				screen.queryByRole("heading", { name: "第 1 题 · 作答与评分依据" }),
			).toBeNull(),
		);
		fireEvent.click(screen.getByRole("button", { name: "查看本题当前成绩" }));
		await screen.findByRole("heading", { name: "第 1 题 · 作答与评分依据" });
	});
	it("无页不显示无效工具或禁用优秀答卷按钮，仍能去文字反馈", async () => {
		installApi((call) => resultRoute(call, makeResult()));
		render(<App />);
		await screen.findByLabelText("总分");
		expect(screen.queryByRole("button", { name: "放大" })).toBeNull();
		expect(screen.queryByRole("button", { name: "查看高分试卷" })).toBeNull();
		expect(screen.getByText("学校尚未提供可查看的优秀答卷。")).toBeTruthy();
		const feedback = screen.getByRole("button", { name: "查看题目文字反馈" });
		const originalHash = window.location.hash;
		const target = document.getElementById("question-review")!;
		const scroll = vi.fn();
		target.scrollIntoView = scroll;
		fireEvent.click(feedback);
		await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
		expect(window.location.hash).toBe(originalHash);
		expect(screen.getByLabelText("总分")).toBeTruthy();
		expect(scroll).toHaveBeenCalledWith({ behavior: "smooth" });
		expect(document.activeElement).toBe(target);
	});
	it("学生缺查分权限只切换身份，不发成绩请求并清理退出账号草稿", async () => {
		const owner = studentDraftOwner(studentUser);
		writeAppealDraft(
			appealDraftKey(owner, "exam-1", "release-7", "question-1"),
			{ reasonCode: "other", reason: "这是一份退出时应清理的草稿" },
		);
		const calls = installApi(
			(call) =>
				call.url.pathname.endsWith("logout") ? jsonResponse({}) : undefined,
			{ ...studentUser, permissions: [] },
		);
		render(<App />);
		await screen.findByRole("heading", { name: "学生账号尚未开通查分权限" });
		expect(calls).toHaveLength(1);
		fireEvent.click(screen.getByRole("button", { name: "切换账号" }));
		await screen.findByRole("heading", { name: "查看已发布成绩" });
		expect(listAppealDrafts(owner, "exam-1")).toHaveLength(0);
	});
	it("同一404按请求上下文解释，401仍要求重新登录", () => {
		const error = new PortalApiError(404, "not_found", "内部ID不应展示");
		expect(friendlyError(error, "question")).toContain("本题详情");
		expect(friendlyError(error, "appeal")).toContain("复核申请");
		expect(friendlyError(error, "image")).toContain("本页图像");
		expect(friendlyError(error)).toContain("这场考试");
		expect(
			friendlyError(new PortalApiError(401, "unauthorized", ""), "question"),
		).toContain("重新登录");
	});
});
