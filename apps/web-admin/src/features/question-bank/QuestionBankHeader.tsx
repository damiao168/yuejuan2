import { Button, Input, Pagination, Select, Space } from "antd";
import { Filter, Plus, RefreshCw } from "lucide-react";
import type { QuestionBank } from "@edugrade/sdk";

export function QuestionBankHeader({ canCreate, loading, banks, bankId, selectBank, bankStatus, kind, bankOffset, total,
  onRefresh, onCreateBank, onCreateItem, onSearchOpen, onSearch, onBankPageChange,
}: Readonly<{
  canCreate: boolean; loading: boolean; banks?: QuestionBank[]; bankId: string;
  selectBank: (id: string) => void; bankStatus?: string; kind: "question" | "rubric_template"; bankOffset: number; total: number;
  onRefresh: () => void; onCreateBank: () => void; onCreateItem: () => void; onSearchOpen: () => void;
  onSearch: (query: string) => void; onBankPageChange: (page: number) => void;
}>) {
  return <>
			<section className="question-bank-heading">
				<div>
					<h1>题库</h1>
					<p>维护题目、答案和评分标准。发布后供授权教师选入考试；已开展考试使用的版本不随题库更新改变。</p>
				</div>
				<Space>
					<Button icon={<RefreshCw size={15} />} onClick={() => onRefresh()}>
						刷新
					</Button>
					{canCreate && (
						<Button icon={<Plus size={15} />} onClick={() => onCreateBank()}>
							新建题库
						</Button>
					)}
				</Space>
			</section>
			<div className="question-bank-toolbar">
				<Select
					aria-label="选择题库"
					placeholder={loading ? "正在加载题库" : "选择题库"}
					value={bankId || undefined}
					onChange={selectBank}
					options={banks?.map((b) => ({
						value: b.id,
						label: `${b.name}${b.status === "archived" ? "（已归档）" : ""}`,
					}))}
				/>
				<Input.Search
					aria-label="搜索题目编号"
					placeholder="搜索题目编号"
					onSearch={onSearch}
					allowClear
					disabled={!bankId}
				/>
				<Button icon={<Filter size={15} />} disabled={!bankId} onClick={() => onSearchOpen()}>
					组合检索
				</Button>
				{canCreate && (
					<Button
						type="primary"
						icon={<Plus size={15} />}
						disabled={!bankId || bankStatus !== "active"}
						onClick={() => onCreateItem()}
					>
						{kind === "rubric_template" ? "新建评分标准模板" : "新建题目"}
					</Button>
				)}
			</div>
			{total > 20 && (
				<Pagination
					size="small"
					current={bankOffset / 20 + 1}
					pageSize={20}
					total={total}
					showSizeChanger={false}
					onChange={onBankPageChange}
				/>
			)}
  </>;
}
