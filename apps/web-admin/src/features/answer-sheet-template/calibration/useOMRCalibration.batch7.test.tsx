// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
	AnswerSheetTemplate,
	OMRCalibrationDetail
} from "../../../api/configuration";
import { useOMRCalibration } from "./useOMRCalibration";
import {
	answerTemplate,
	calibrationDetail
} from "../testing/answerSheetFixtures";

const mocks = vi.hoisted(() => ({
	list: vi.fn(),
	get: vi.fn(),
	create: vi.fn(),
	approve: vi.fn(),
	revoke: vi.fn(),
	discard: vi.fn(),
	label: vi.fn(),
	image: vi.fn(),
	message: { error: vi.fn(), warning: vi.fn(), success: vi.fn() }
}));
vi.mock("../../../api/configuration", () => ({
	listOMRCalibrations: mocks.list,
	getOMRCalibration: mocks.get,
	createOMRCalibration: mocks.create,
	approveOMRCalibration: mocks.approve,
	revokeOMRCalibration: mocks.revoke,
	discardOMRCalibration: mocks.discard,
	labelOMRCalibrationCase: mocks.label,
	downloadOMRCalibrationCaseImage: mocks.image
}));
const template: AnswerSheetTemplate = { ...answerTemplate, status: "locked" };
const detail: OMRCalibrationDetail = { ...calibrationDetail(), cases: [] };
const formatError = (error: unknown) => (error as Error).message;
function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (reason: unknown) => void;
	const promise = new Promise<T>((yes, no) => {
		resolve = yes;
		reject = no;
	});
	return { promise, resolve, reject };
}

describe("calibration action and list behavior against the original hook", () => {
	let root: Root,
		host: HTMLDivElement,
		current: ReturnType<typeof useOMRCalibration>;
	function Probe({
		selectedTemplate = template
	}: Readonly<{ selectedTemplate?: AnswerSheetTemplate }>) {
		current = useOMRCalibration({
			template: selectedTemplate,
			enabled: true,
			message: mocks.message,
			formatError
		});
		return null;
	}
	beforeEach(() => {
		vi.resetAllMocks();
		Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
		mocks.list.mockResolvedValue({ calibrations: [] });
		mocks.get.mockResolvedValue({ calibration: detail });
		for (const action of [mocks.approve, mocks.revoke, mocks.discard])
			action.mockResolvedValue({ calibration: detail });
		host = document.createElement("div");
		document.body.append(host);
		root = createRoot(host);
	});
	afterEach(async () => {
		await act(async () => root.unmount());
		host.remove();
	});
	async function mount(selectedTemplate = template) {
		await act(async () =>
			root.render(<Probe selectedTemplate={selectedTemplate} />)
		);
	}

	it.each([
		["approve", "校准已批准，此后整套模板的高把握识别结果可自动确认"],
		["revoke", "校准已撤销，未完成的识别任务将转入人工复核"],
		["discard", "草稿已弃用，标注记录会保留备查"]
	] as const)(
		"dispatches %s with the trimmed reason and refreshes before closing",
		async (action, success) => {
			await mount();
			await act(async () => current.open("session"));
			await act(async () => {
				current.setAction(action);
				current.setActionReason("  本次操作原因需要记录备查  ");
			});
			await act(async () => current.submitAction());
			const selected = {
				approve: mocks.approve,
				revoke: mocks.revoke,
				discard: mocks.discard
			}[action];
			expect(selected).toHaveBeenCalledWith(
				"session",
				"本次操作原因需要记录备查"
			);
			expect(
				[mocks.approve, mocks.revoke, mocks.discard].filter(
					(fn) => fn.mock.calls.length
				)
			).toHaveLength(1);
			expect(mocks.list).toHaveBeenLastCalledWith("template");
			expect(current.action).toBeUndefined();
			expect(current.busy).toBe(false);
			expect(mocks.message.success).toHaveBeenCalledWith(success);
		}
	);

	it("retains the selected action and reason after failure so an explicit retry remains possible", async () => {
		mocks.approve.mockRejectedValueOnce(new Error("network failed"));
		await mount();
		await act(async () => current.open("session"));
		await act(async () => {
			current.setAction("approve");
			current.setActionReason("本次操作原因需要记录备查");
		});
		await act(async () => current.submitAction());
		expect(current.action).toBe("approve");
		expect(current.actionReason).toBe("本次操作原因需要记录备查");
		expect(current.busy).toBe(false);
		expect(mocks.message.error).toHaveBeenCalledWith("network failed");
		expect(mocks.message.success).not.toHaveBeenCalled();
	});

	it("does not send an action without the required reason", async () => {
		await mount();
		await act(async () => current.open("session"));
		await act(async () => {
			current.setAction("approve");
			current.setActionReason(" 太短 ");
		});
		await act(async () => current.submitAction());
		expect(mocks.approve).not.toHaveBeenCalled();
		expect(current.busy).toBe(false);
		expect(mocks.message.error).toHaveBeenCalledWith(
			"请填写操作原因（至少 10 个字），将记入操作记录"
		);
	});

	it("ignores a superseded list failure after a different template list has loaded", async () => {
		const waiting = deferred<{ calibrations: [] }>();
		mocks.list
			.mockReturnValueOnce(waiting.promise)
			.mockResolvedValueOnce({ calibrations: [{ id: "new-session" }] });
		await mount();
		await mount({ ...template, id: "other-template" });
		await act(async () => waiting.reject(new Error("old list failed")));
		expect(current.calibrations).toEqual([{ id: "new-session" }]);
		expect(current.error).toBeUndefined();
		expect(current.loading).toBe(false);
	});
});
