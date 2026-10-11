import { useState } from "react";
import { listAppealDrafts, removeAppealDraft } from "../../appealDrafts";
import { useStudentSession } from "../../studentSession";

export function OldAppealDrafts({
	examID,
	releaseID,
}: Readonly<{ examID: string; releaseID: string }>) {
	const { draftOwner, submitting } = useStudentSession();
	const [revision, setRevision] = useState(0);
	const [copyStatus, setCopyStatus] = useState("");
	const drafts = listAppealDrafts(draftOwner, examID).filter(
		(item) => item.releaseID !== releaseID,
	);
	if (!drafts.length) return null;
	return (
		<details key={revision} className="old-appeal-drafts">
			<summary>旧版复核草稿（{drafts.length}）</summary>
			<p>
				这些草稿只保存在当前标签页会话，退出登录会清除。说明可复制；旧版圈选不会迁移，新版必须重新核对证据。
			</p>
			{drafts.map((item) => (
				<article key={item.key}>
					<h3>
						{item.draft.questionNo
							? `第 ${item.draft.questionNo} 题`
							: "原题目"}{" "}
						·{" "}
						{item.draft.releaseVersion
							? `第 ${item.draft.releaseVersion} 版`
							: `原发布 ${item.releaseID}`}
					</h3>
					<textarea
						aria-label="旧版复核说明"
						rows={4}
						readOnly
						value={item.draft.reason}
					/>
					<button
						type="button"
						disabled={submitting}
						onClick={() =>
							void Promise.resolve()
								.then(() => navigator.clipboard.writeText(item.draft.reason))
								.then(() =>
									setCopyStatus("旧说明已复制，请核对新版题目后自行填写。"),
								)
								.catch(() => setCopyStatus("复制失败，请选中原说明手动复制。"))
						}
					>
						复制旧说明
					</button>
					<button
						type="button"
						disabled={submitting}
						onClick={() => {
							removeAppealDraft(item.key);
							setRevision((value) => value + 1);
						}}
					>
						删除此草稿
					</button>
				</article>
			))}
			{copyStatus ? <p><output aria-live="polite" aria-atomic="true">{copyStatus}</output></p> : null}
		</details>
	);
}
