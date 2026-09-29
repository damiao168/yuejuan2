import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
	Alert,
	App,
	Button,
	Empty,
	Input,
	InputNumber,
	Modal,
	Select,
	Space,
} from "antd";
import type { QuestionBankVersion } from "@edugrade/sdk";
import { questionBankApi } from "../../api/questionBank";
import { getExam } from "../../api/exams";
import { ApiClientError, getUserErrorMessage } from "../../api/client";
import { ResponsiveTable } from "../../components/ResponsiveTable";

export function BankQuestionPicker({
	examId,
	nextOrder,
	onCopied,
	scopeKey,
}: {
	examId: string;
	nextOrder: number;
	onCopied: () => Promise<void>;
	scopeKey: string;
}) {
	const { message } = App.useApp();
	const [open, setOpen] = useState(false);
	const [bankId, setBankId] = useState("");
	const [itemId, setItemId] = useState("");
	const [version, setVersion] = useState<QuestionBankVersion | null>(null);
	const [number, setNumber] = useState("");
	const [order, setOrder] = useState(nextOrder);
	const [offset, setOffset] = useState(0);
	const [bankOffset, setBankOffset] = useState(0);
	const [query, setQuery] = useState("");
	const [saving, setSaving] = useState(false);
	const pending = useRef<{
		fingerprint: string;
		key: string;
		revision: number;
	} | null>(null);
	const banks = useQuery({
		queryKey: ["bank-picker", scopeKey, examId, "banks", bankOffset],
		queryFn: ({ signal }) =>
			questionBankApi.listQuestionBanks({
				query: { limit: 20, offset: bankOffset },
				signal,
			}),
		enabled: open,
	});
	const items = useQuery({
		queryKey: ["bank-picker", scopeKey, examId, bankId, query, offset],
		queryFn: ({ signal }) =>
			questionBankApi.listQuestionBankItems({
				path: { bankId },
				query: { q: query, limit: 20, offset },
				signal,
			}),
		enabled: open && Boolean(bankId),
	});
	const versions = useQuery({
		queryKey: ["bank-picker", scopeKey, examId, "versions", itemId],
		queryFn: ({ signal }) =>
			questionBankApi.listQuestionBankVersions({
				path: { itemId },
				query: { limit: 100 },
				signal,
			}),
		enabled: open && Boolean(itemId),
	});
	async function copy() {
		if (!version || !number.trim()) return;
		setSaving(true);
		try {
			const fingerprint = JSON.stringify({
				examId,
				versionId: version.id,
				number,
				order,
			});
			// 响应未确定时沿用原幂等键和考试修订号重试；只有重新选择内容或服务端明确冲突，才创建新请求。
			if (pending.current?.fingerprint !== fingerprint) {
				const latest = await getExam(examId);
				pending.current = {
					fingerprint,
					key: crypto.randomUUID(),
					revision: latest.exam.revision,
				};
			}
			const body = {
				expected_revision: pending.current.revision,
				selections: [
					{
						version_id: version.id,
						question_no: number.trim(),
						sort_order: order,
					},
				],
			};
			await questionBankApi.materializeQuestionBankVersions({
				path: { examId },
				body,
				headers: { "Idempotency-Key": pending.current.key },
			});
			pending.current = null;
			setOpen(false);
			await onCopied();
			message.success("题目及评分标准已复制，请继续配置答题区域与开考检查");
		} catch (error) {
			if (error instanceof ApiClientError && error.status === 409)
				pending.current = null;
			message.error(
				getUserErrorMessage(error, "复制失败，请核对考试状态、题号与题库授权"),
			);
		} finally {
			setSaving(false);
		}
	}
	return (
		<>
			<Button
				onClick={() => {
					setOpen(true);
					setVersion(null);
					setNumber("");
					setOrder(nextOrder);
				}}
			>
				从题库选题
			</Button>
			<Modal
				title="从题库选用已发布版本"
				open={open}
				onCancel={() => !saving && setOpen(false)}
				width={880}
				footer={
					<Button
						type="primary"
						disabled={!version || !number.trim() || saving}
						loading={saving}
						onClick={() => void copy()}
					>
						复制到考试
					</Button>
				}
				destroyOnHidden
			>
				<Alert
					type="info"
					message="选用具体发布版本，复制题干、答案、解析与评分标准。复制后继续配置考试答题区域并完成开考检查。"
				/>
				<Space wrap style={{ margin: "16px 0" }}>
					<Select
						aria-label="选题题库"
						placeholder="选择题库"
						value={bankId || undefined}
						style={{ minWidth: 200 }}
						options={banks.data?.banks
							.filter((b) => b.status === "active")
							.map((b) => ({ value: b.id, label: b.name }))}
						onChange={(id) => {
							setBankId(id);
							setItemId("");
							setVersion(null);
							setOffset(0);
						}}
					/>
					<Button
						disabled={bankOffset === 0}
						onClick={() => setBankOffset(Math.max(0, bankOffset - 20))}
					>
						上一页题库
					</Button>
					<Button
						disabled={bankOffset + 20 >= (banks.data?.total ?? 0)}
						onClick={() => setBankOffset(bankOffset + 20)}
					>
						下一页题库
					</Button>
					<Input.Search
						placeholder="按题目编号搜索"
						onSearch={(q) => {
							setQuery(q);
							setOffset(0);
						}}
					/>
				</Space>
				{banks.isError || items.isError || versions.isError ? (
					<Alert type="error" message="题库加载失败，请重新打开选题窗口" />
				) : null}
				<ResponsiveTable
					className="dense-data-table"
					rowKey="id"
					loading={items.isLoading}
					dataSource={items.data?.items ?? []}
					columns={[
						{ title: "编号", dataIndex: "item_code" },
						{ title: "学科", dataIndex: "subject_code" },
						{
							title: "版本",
							render: (_, item) => (
								<Button
									disabled={!item.current_published_version_id}
									onClick={() => {
										setItemId(item.id);
										setVersion(null);
									}}
								>
									查看发布版本
								</Button>
							),
						},
					]}
					pagination={{
						current: offset / 20 + 1,
						pageSize: 20,
						total: items.data?.total,
						showSizeChanger: false,
						onChange: (p) => setOffset((p - 1) * 20),
					}}
				/>
				{itemId ? (
					<Select
						aria-label="选用发布版本"
						placeholder="选择具体版本"
						value={version?.id}
						style={{ width: "100%", marginTop: 16 }}
						options={versions.data?.versions
							.filter((v) => v.workflow_status === "published")
							.map((v) => ({
								value: v.id,
								label: `v${v.version_no} · ${v.default_score} 分`,
							}))}
						onChange={(id) =>
							setVersion(
								versions.data?.versions.find((v) => v.id === id) ?? null,
							)
						}
					/>
				) : (
					<Empty description="选择题目，查看可用发布版本" />
				)}
				{version ? (
					<section className="question-bank-preview">
						<p>{version.stem}</p>
						{version.options.map((o, i) => (
							<p key={`${i}:${o}`}>
								{String.fromCharCode(65 + i)}. {o}
							</p>
						))}
						<p>
							{version.default_score} 分 · 评分点{" "}
							{version.scoring.rubric?.points.length ?? 0} 个
						</p>
						<Space wrap>
							<label>
								考试题号{" "}
								<Input
									value={number}
									maxLength={32}
									onChange={(e) => setNumber(e.target.value)}
								/>
							</label>
							<label>
								排列顺序{" "}
								<InputNumber
									value={order}
									min={1}
									precision={0}
									onChange={(n) => setOrder(n ?? 1)}
								/>
							</label>
						</Space>
					</section>
				) : null}
			</Modal>
		</>
	);
}
