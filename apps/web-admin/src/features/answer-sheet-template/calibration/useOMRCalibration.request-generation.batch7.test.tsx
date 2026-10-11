// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
	AnswerSheetTemplate,
	OMRCalibrationDetail
} from "../../../api/configuration";
import {
	answerTemplate,
	calibrationDetail
} from "../testing/answerSheetFixtures";
import { useOMRCalibration } from "./useOMRCalibration";

const mocks = vi.hoisted(() => ({
	list: vi.fn(),
	get: vi.fn(),
	create: vi.fn(),
	label: vi.fn(),
	approve: vi.fn(),
	revoke: vi.fn(),
	discard: vi.fn(),
	image: vi.fn(),
	message: { error: vi.fn(), warning: vi.fn(), success: vi.fn() }
}));
vi.mock("../../../api/configuration", () => ({
	listOMRCalibrations: mocks.list,
	getOMRCalibration: mocks.get,
	createOMRCalibration: mocks.create,
	labelOMRCalibrationCase: mocks.label,
	approveOMRCalibration: mocks.approve,
	revokeOMRCalibration: mocks.revoke,
	discardOMRCalibration: mocks.discard,
	downloadOMRCalibrationCaseImage: mocks.image
}));

const template: AnswerSheetTemplate = { ...answerTemplate, status: "locked" };
const detail = calibrationDetail();
type Operation = "open" | "start" | "label" | "approve" | "revoke" | "discard";
function deferred<T>() {
	let resolve!: (value: T) => void;
	let reject!: (reason: unknown) => void;
	const promise = new Promise<T>((yes, no) => {
		resolve = yes;
		reject = no;
	});
	return { promise, resolve, reject };
}
const formatError = (error: unknown) => (error as Error).message;

describe("calibration request context defect, separately from equivalent refactoring", () => {
	let root: Root;
	let host: HTMLDivElement;
	let mounted: boolean;
	let current: ReturnType<typeof useOMRCalibration>;
	function Probe({
		selectedTemplate = template,
		enabled = true
	}: Readonly<{ selectedTemplate?: AnswerSheetTemplate; enabled?: boolean }>) {
		current = useOMRCalibration({
			template: selectedTemplate,
			enabled,
			message: mocks.message,
			formatError
		});
		return null;
	}
	beforeEach(() => {
		vi.resetAllMocks();
		Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
		mocks.list.mockResolvedValue({ calibrations: [] });
		for (const api of [
			mocks.get,
			mocks.create,
			mocks.label,
			mocks.approve,
			mocks.revoke,
			mocks.discard
		])
			api.mockResolvedValue({ calibration: detail });
		mocks.image.mockResolvedValue({ blob: new Blob(["sample"]) });
		URL.createObjectURL = vi.fn(() => "blob:sample");
		URL.revokeObjectURL = vi.fn();
		host = document.createElement("div");
		document.body.append(host);
		root = createRoot(host);
		mounted = true;
	});
	afterEach(async () => {
		if (mounted) await act(async () => root.unmount());
		host.remove();
	});
	async function mount(selectedTemplate = template, enabled = true) {
		await act(async () =>
			root.render(
				<Probe selectedTemplate={selectedTemplate} enabled={enabled} />
			)
		);
	}
	async function begin(
		operation: Operation,
		response: Promise<{ calibration: OMRCalibrationDetail }>
	) {
		await mount();
		if (operation !== "open" && operation !== "start")
			await act(async () => current.open("session"));
		if (
			operation === "approve" ||
			operation === "revoke" ||
			operation === "discard"
		)
			await act(async () => {
				current.setAction(operation);
				current.setActionReason("本次操作原因需要记录备查");
			});
		const api = {
			open: mocks.get,
			start: mocks.create,
			label: mocks.label,
			approve: mocks.approve,
			revoke: mocks.revoke,
			discard: mocks.discard
		}[operation];
		api.mockReturnValueOnce(response);
		let pending!: Promise<void>;
		await act(async () => {
			if (operation === "open") pending = current.open("session");
			else if (operation === "start") pending = current.start();
			else if (operation === "label") pending = current.labelCase(["A"]);
			else pending = current.submitAction();
		});
		expect(current.busy).toBe(true);
		return { pending, api };
	}

	it.each(["open", "start", "label", "approve", "revoke", "discard"] as const)(
		"does not reopen old %s detail or publish success after a template switch",
		async (operation) => {
			const waiting = deferred<{ calibration: OMRCalibrationDetail }>();
			const { pending, api } = await begin(operation, waiting.promise);
			await mount({ ...template, id: "other-template" });
			await act(async () => {
				waiting.resolve({ calibration: detail });
				await pending;
			});
			expect(api).toHaveBeenCalled();
			expect(current.detail).toBeUndefined();
			expect(current.drawerOpen).toBe(false);
			expect(current.busy).toBe(false);
			expect(current.action).toBeUndefined();
			expect(current.actionReason).toBe("");
			expect(mocks.list).toHaveBeenLastCalledWith("other-template");
			expect(mocks.message.success).not.toHaveBeenCalled();
			expect(mocks.message.warning).not.toHaveBeenCalled();
		}
	);

	it.each(["open", "start", "label", "approve", "revoke", "discard"] as const)(
		"ignores a stale %s failure in another template context",
		async (operation) => {
			const waiting = deferred<{ calibration: OMRCalibrationDetail }>();
			const { pending } = await begin(operation, waiting.promise);
			await mount({ ...template, id: "other-template" });
			await act(async () => {
				waiting.reject(new Error("old context failed"));
				await pending;
			});
			expect(mocks.message.error).not.toHaveBeenCalled();
			expect(current.busy).toBe(false);
			expect(current.detail).toBeUndefined();
		}
	);

	it("invalidates pending detail when calibration permission is removed", async () => {
		const waiting = deferred<{ calibration: OMRCalibrationDetail }>();
		const { pending } = await begin("open", waiting.promise);
		await mount(template, false);
		await act(async () => {
			waiting.resolve({ calibration: detail });
			await pending;
		});
		expect(current.detail).toBeUndefined();
		expect(current.drawerOpen).toBe(false);
		expect(current.busy).toBe(false);
	});

	it("does not publish notifications or trigger an old refresh after unmount", async () => {
		const waiting = deferred<{ calibration: OMRCalibrationDetail }>();
		const { pending } = await begin("start", waiting.promise);
		await act(async () => root.unmount());
		mounted = false;
		await act(async () => {
			waiting.resolve({ calibration: detail });
			await pending;
		});
		expect(mocks.list).toHaveBeenCalledTimes(1);
		expect(mocks.message.success).not.toHaveBeenCalled();
		expect(mocks.message.error).not.toHaveBeenCalled();
	});

	it("keeps a newer detail request busy when the previous request completes", async () => {
		const first = deferred<{ calibration: OMRCalibrationDetail }>();
		const second = deferred<{ calibration: OMRCalibrationDetail }>();
		await mount();
		mocks.get
			.mockReturnValueOnce(first.promise)
			.mockReturnValueOnce(second.promise);
		let old!: Promise<void>, latest!: Promise<void>;
		await act(async () => {
			old = current.open("old-session");
			latest = current.open("new-session");
		});
		await act(async () => {
			first.resolve({ calibration: detail });
			await old;
		});
		expect(current.busy).toBe(true);
		expect(current.detail).toBeUndefined();
		const newest = {
			...detail,
			session: { ...detail.session, id: "new-session" }
		};
		await act(async () => {
			second.resolve({ calibration: newest });
			await latest;
		});
		expect(current.detail?.session.id).toBe("new-session");
		expect(current.busy).toBe(false);
	});

	it("checks context again after the mutation refresh before publishing success", async () => {
		const refresh = deferred<{ calibrations: [] }>();
		await mount();
		mocks.list.mockReturnValueOnce(refresh.promise);
		let pending!: Promise<void>;
		await act(async () => {
			pending = current.start();
		});
		await mount({ ...template, id: "other-template" });
		await act(async () => {
			refresh.resolve({ calibrations: [] });
			await pending;
		});
		expect(current.detail).toBeUndefined();
		expect(current.busy).toBe(false);
		expect(mocks.message.success).not.toHaveBeenCalled();
	});
});
