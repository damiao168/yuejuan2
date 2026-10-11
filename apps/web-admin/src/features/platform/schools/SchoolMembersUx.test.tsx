// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { SchoolMembersTab } from "./SchoolMembersTab";
const api = vi.hoisted(() => ({ members: vi.fn() }));
vi.mock("../../../api/platformSchools", () => ({
	listPlatformSchoolMembers: api.members,
}));
vi.mock("antd", () => ({
	Alert: ({ message }: { message: ReactNode }) => <p>{message}</p>,
	Spin: () => null,
	Tooltip: ({ children }: { children: ReactNode }) => <>{children}</>,
	Input: () => null,
	Select: ({
		"aria-label": label,
		value,
		onChange,
		options,
	}: {
		"aria-label": string;
		value: string;
		onChange: (value: string) => void;
		options: { value: string; label: string }[];
	}) => (
		<select
			aria-label={label}
			value={value}
			onChange={(event) => onChange(event.target.value)}
		>
			{options.map((item) => (
				<option key={item.value} value={item.value}>
					{item.label}
				</option>
			))}
		</select>
	),
}));
vi.mock("../../../components/ResponsiveTable", () => ({
	ResponsiveTable: ({
		dataSource,
		columns,
	}: {
		dataSource: Record<string, unknown>[];
		columns: {
			dataIndex?: string;
			render?: (value: unknown, item: Record<string, unknown>) => ReactNode;
		}[];
	}) => (
		<table>
			<tbody>
				{dataSource.map((item) => (
					<tr key={String(item.id)}>
						{columns.map((col, index) => (
							<td key={index}>
								{col.render
									? col.render(item[col.dataIndex ?? ""], item)
									: String(item[col.dataIndex ?? ""] ?? "")}
							</td>
						))}
					</tr>
				))}
			</tbody>
		</table>
	),
}));
beforeEach(() =>
	api.members.mockResolvedValue({
		summary: {
			total: 4,
			active: 1,
			invited: 1,
			disabled: 1,
			unknown: 1,
			admins: 0,
		},
		members: [
			{
				id: "a",
				username: "active",
				display_name: "正常用户",
				status: "active",
				roles: [],
			},
			{
				id: "b",
				username: "invited",
				display_name: "邀请用户",
				status: "invited",
				roles: ["teacher"],
				created_at: "2026-10-01T00:00:00Z",
				activation_expires_at: "2020-10-02T00:00:00Z",
			},
			{
				id: "c",
				username: "disabled",
				display_name: "停用用户",
				status: "disabled",
				roles: [],
			},
			{
				id: "d",
				username: "future",
				display_name: "未知用户",
				status: "future",
				roles: [],
			},
		],
	}),
);
describe("平台学校成员真实邀请状态", () => {
	it("待激活不计停用，过期使用真实邀请到期日并保留只读边界", async () => {
		render(
			<SchoolMembersTab tenantId="school" studentCount={5} focus={null} />,
		);
		await screen.findByText("邀请用户");
		expect(screen.getByText(/1 待激活 · 1 已停用/)).toBeTruthy();
		expect(screen.getAllByText(/已过期/).length).toBeGreaterThan(0);
		fireEvent.change(screen.getByLabelText("账号状态"), {
			target: { value: "invited" },
		});
		expect(screen.getByText("邀请用户")).toBeTruthy();
		expect(screen.queryByText("停用用户")).toBeNull();
		fireEvent.change(screen.getByLabelText("账号状态"), {
			target: { value: "unknown" },
		});
		expect(screen.getByText("未知用户")).toBeTruthy();
		expect(screen.getAllByText("未知状态").length).toBeGreaterThan(0);
		expect(screen.queryByText("邀请用户")).toBeNull();
		expect(screen.queryByRole("button", { name: /重发|恢复|停用/ })).toBeNull();
		expect(api.members).toHaveBeenCalledWith("school");
	});
});
