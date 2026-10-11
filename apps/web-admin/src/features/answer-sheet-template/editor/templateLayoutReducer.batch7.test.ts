import { describe, expect, it } from "vitest";
import type { LayoutRegion, TemplateLayout } from "../../../api/configuration";
import {
	templateLayoutReducer,
	type TemplateLayoutAction
} from "./templateLayoutReducer";

const region = (id: string, question_id?: string): LayoutRegion => ({
	id,
	question_id,
	x: 0.1,
	y: 0.2,
	width: 0.3,
	height: 0.4
});
function layout(): TemplateLayout {
	return {
		omr_profile: {
			mode: "template_difference",
			version: "opencv-template-difference-bubble-v1",
			reference: {
				source: "exam_paper",
				file_asset_id: "asset",
				hash_sha256: "hash",
				content_type: "application/pdf"
			}
		},
		pages: [
			{
				page_no: 1,
				width: 100,
				height: 200,
				registration_marks: [],
				identity_regions: [],
				question_regions: [region("r1", "q1"), region("unassigned")]
			},
			{
				page_no: 2,
				width: 100,
				height: 200,
				registration_marks: [],
				identity_regions: [],
				question_regions: [region("r2", "q2")]
			},
			{
				page_no: 3,
				width: 100,
				height: 200,
				registration_marks: [],
				identity_regions: [],
				question_regions: []
			}
		]
	};
}

describe("layout action boundaries against the original reducer", () => {
	it("preserves direct and functional set results by identity", () => {
		const original = layout(),
			replacement = layout();
		expect(
			templateLayoutReducer(original, { type: "set", value: replacement })
		).toBe(replacement);
		expect(
			templateLayoutReducer(original, {
				type: "set",
				value: (current) => current
			})
		).toBe(original);
	});

	it.each(["manual_only", "template_difference"] as const)(
		"sets the %s profile version and clears the previous reference",
		(mode) => {
			const original = layout();
			const next = templateLayoutReducer(original, {
				type: "setOMRProfile",
				mode
			});
			expect(next.omr_profile).toEqual({
				mode,
				version:
					mode === "manual_only"
						? "opencv-fill-v1"
						: "opencv-template-difference-bubble-v1"
			});
			expect(next.pages).toBe(original.pages);
			expect(original.omr_profile?.reference?.file_asset_id).toBe("asset");
		}
	);

	it.each([
		{ type: "updateRegion", regionId: "missing", patch: { x: 0.5 } },
		{ type: "removeRegion", regionId: "missing" },
		{
			type: "updateOption",
			regionId: "missing",
			optionId: "a",
			patch: { x: 0.5 }
		},
		{
			type: "addOption",
			regionId: "missing",
			option: { id: "a", label: "A", x: 0, y: 0, width: 0.1, height: 0.1 }
		},
		{ type: "removeOption", regionId: "missing", optionId: "a" },
		{ type: "addRegion", pageNo: 99, region: region("new", "q3") },
		{ type: "suggestRegions", pages: new Map() }
	] satisfies TemplateLayoutAction[])(
		"retains every page identity when $type matches nothing",
		(action) => {
			const original = layout();
			const snapshot = structuredClone(original);
			const next = templateLayoutReducer(original, action);
			expect(next).not.toBe(original);
			expect(next.pages).not.toBe(original.pages);
			original.pages.forEach((page, index) =>
				expect(next.pages[index]).toBe(page)
			);
			expect(original).toEqual(snapshot);
		}
	);

	it("moves a duplicated question and retains every unrelated page and region", () => {
		const original = layout();
		original.pages[1].question_regions.push(region("duplicate", "q1"));
		const next = templateLayoutReducer(original, {
			type: "replaceQuestionRegion",
			questionId: "q1",
			pageNo: 2,
			region: region("new", "q1")
		});
		expect(next.pages[0].question_regions.map((item) => item.id)).toEqual([
			"unassigned"
		]);
		expect(next.pages[1].question_regions.map((item) => item.id)).toEqual([
			"r2",
			"new"
		]);
		expect(next.pages[1].question_regions[0]).toBe(
			original.pages[1].question_regions[0]
		);
		expect(next.pages[2]).toBe(original.pages[2]);
		expect(original.pages[0].question_regions[0].id).toBe("r1");
	});

	it("creates option arrays only on the matched region and preserves other option identities", () => {
		const original = layout();
		const added = templateLayoutReducer(original, {
			type: "addOption",
			regionId: "r1",
			option: { id: "a", label: "A", x: 0, y: 0, width: 0.1, height: 0.1 }
		});
		const withOther = templateLayoutReducer(added, {
			type: "addOption",
			regionId: "r1",
			option: { id: "b", label: "B", x: 0.2, y: 0, width: 0.1, height: 0.1 }
		});
		const edited = templateLayoutReducer(withOther, {
			type: "updateOption",
			regionId: "r1",
			optionId: "a",
			patch: { label: "C", width: 0.2 }
		});
		expect(edited.pages[0].question_regions[0].option_regions).toMatchObject([
			{ id: "a", label: "C", width: 0.2 },
			{ id: "b" }
		]);
		expect(edited.pages[0].question_regions[0].option_regions?.[1]).toBe(
			withOther.pages[0].question_regions[0].option_regions?.[1]
		);
		const removed = templateLayoutReducer(edited, {
			type: "removeOption",
			regionId: "r1",
			optionId: "a"
		});
		expect(
			removed.pages[0].question_regions[0].option_regions?.map(
				(item) => item.id
			)
		).toEqual(["b"]);
		expect(removed.pages[1]).toBe(original.pages[1]);
		expect(
			original.pages[0].question_regions[0].option_regions
		).toBeUndefined();
	});

	it("keeps unassigned and unmatched manual regions when applying suggestions", () => {
		const original = layout();
		const suggested = region("suggested", "q1");
		const next = templateLayoutReducer(original, {
			type: "suggestRegions",
			pages: new Map([
				[1, [suggested]],
				[2, []]
			])
		});
		expect(next.pages[0].question_regions).toEqual([
			original.pages[0].question_regions[1],
			suggested
		]);
		expect(next.pages[1]).toBe(original.pages[1]);
		expect(next.pages[2]).toBe(original.pages[2]);
	});
});
