from pathlib import Path
replacements={
'apps/web-admin/src/components/appLayoutPresentation.test.tsx': [('expect(markup(<ShellTopbar {...topbar} />).children.length).toBe(0);','expect(markup(<ShellTopbar {...topbar} />).children).toHaveLength(0);')],
'apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.test.tsx':[('expect(mocks.groups.mock.calls.filter((call) => call[1] === "A").length).toBe(callsForA);','expect(mocks.groups.mock.calls.filter((call) => call[1] === "A")).toHaveLength(callsForA);')],
'apps/web-admin/src/features/question-bank/QuestionBankPage.batch45.test.tsx':[('expect(uuid.mock.calls.length).toBe(createdCount);','expect(uuid.mock.calls).toHaveLength(createdCount);')],
'apps/web-admin/src/pages/PlatformModelConfigPage.test.tsx':[('expect(mocks.list.mock.calls.length).toBe(listCalls);','expect(mocks.list.mock.calls).toHaveLength(listCalls);'),('expect(mocks.schools.mock.calls.length).toBe(schoolCalls);','expect(mocks.schools.mock.calls).toHaveLength(schoolCalls);')],
 'tests/e2e/specs/grading-workbench-keyboard.spec.ts':[('expect(await feedback.inputValue()).toBe("");','await expect(feedback).toHaveValue("");')],
 'tests/e2e/specs/platform-model-management.spec.ts':[('expect((runs[0].candidates as unknown[]).length).toBe(2);','expect(runs[0].candidates as unknown[]).toHaveLength(2);')]
}
for f,rs in replacements.items():
 p=Path(f);s=p.read_text(encoding='utf-8')
 for old,new in rs:
  if old not in s: print('NOT FOUND',f,old);continue
  s=s.replace(old,new)
 p.write_text(s,encoding='utf-8')
p=Path('apps/web-admin/src/features/answer-sheet-template/calibration/useOMRCalibration.batch7.test.tsx');s=p.read_text(encoding='utf-8');s=s.replace('''				).length
			).toBe(1);''','''				)
			).toHaveLength(1);''');p.write_text(s,encoding='utf-8')
p=Path('apps/student-portal/src/App.paperRecovery.test.tsx');s=p.read_text(encoding='utf-8');s=s.replace('await act(async () => { render(<PaperViewer examID="exam-1" result={result} onViewCurrent={vi.fn()} />); });\n    expect(screen.getByRole("button", { name: "查看当前成绩并重新核对" })).toBeTruthy();','render(<PaperViewer examID="exam-1" result={result} onViewCurrent={vi.fn()} />);\n    expect(await screen.findByRole("button", { name: "查看当前成绩并重新核对" })).toBeTruthy();').replace('await act(async () => { render(<StrictMode><FirstFrameError /></StrictMode>); });','render(<StrictMode><FirstFrameError /></StrictMode>);');p.write_text(s,encoding='utf-8')
p=Path('apps/web-admin/scripts/measure-build.mjs');s=p.read_text(encoding='utf-8');s=s.replace('tag.match(/\\brel=["\']([^"\']+)["\']/)','/\\brel=["\']([^"\']+)["\']/.exec(tag)').replace('tag.match(/\\bhref=["\']([^"\']+)["\']/)','/\\bhref=["\']([^"\']+)["\']/.exec(tag)');p.write_text(s,encoding='utf-8')
p=Path('scripts/check-story049-ocr-worker.mjs');s=p.read_text(encoding='utf-8');s=s.replace('systemInfo.match(/"capabilities": \\[]string\\{([\\s\\S]*?)\\}/)','/"capabilities": \\[]string\\{([\\s\\S]*?)\\}/.exec(systemInfo)').replace('systemInfo.match(/"not_implemented": \\[]string\\{([\\s\\S]*?)\\}/)','/"not_implemented": \\[]string\\{([\\s\\S]*?)\\}/.exec(systemInfo)');p.write_text(s,encoding='utf-8')
p=Path('apps/web-admin/scripts/check-production-routes.mjs');s=p.read_text(encoding='utf-8');s=s.replace('entryHtml.match(/src=["\']\\/?(assets\\/index-[^"\']+\\.js)["\']/)','/src=["\']\\/?(assets\\/index-[^"\']+\\.js)["\']/.exec(entryHtml)')
s=s.replace('gradingQueue.match(/const scopeQuery[\\s\\S]*?\\n {2}\\}\\), \\[[^\\n]+\\]\\);/)','/const scopeQuery[\\s\\S]*?\\n {2}\\}\\), \\[[^\\n]+\\]\\);/.exec(gradingQueue)').replace('gradingQueue.match(/const reviewerProgress[\\s\\S]*?\\n {2}\\}, \\[[^\\n]+\\]\\);/)','/const reviewerProgress[\\s\\S]*?\\n {2}\\}, \\[[^\\n]+\\]\\);/.exec(gradingQueue)');p.write_text(s,encoding='utf-8')
