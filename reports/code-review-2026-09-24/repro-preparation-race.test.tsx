import React from 'react';
import { render, fireEvent, waitFor, act, cleanup } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { ExamStudentScopePage } from '../../apps/web-admin/src/pages/ExamPreparationPage';
import { getExam, updateExam } from '../../apps/web-admin/src/api/exams';
import { listClasses, listGrades } from '../../apps/web-admin/src/api/org';

vi.mock('../../apps/web-admin/src/api/exams', () => ({ getExam: vi.fn(), updateExam: vi.fn(), refreshExamCandidates: vi.fn() }));
vi.mock('../../apps/web-admin/src/api/org', () => ({
  listClasses: vi.fn(async () => ({ classes: ['A','B'].flatMap(id => [
    { id: `${id}-base`, school_id: id, grade_id: 'none', name: `${id} base` },
    { id: `${id}-extra`, school_id: id, grade_id: 'none', name: `${id} extra` },
  ]) })), listGrades: vi.fn(async () => ({ grades: [] })),
}));
vi.mock('../../apps/web-admin/src/api/configuration', () => ({ getExamReadiness: vi.fn(), confirmExamReadiness: vi.fn(), startExamCollection: vi.fn() }));
vi.mock('antd', async () => {
  const actual = await vi.importActual<any>('antd');
  return { ...actual, App: { useApp: () => ({ message: { success: vi.fn(), error: vi.fn() }, modal: {} }) } };
});
afterEach(cleanup);

// Diagnostic test asserts the current defect; it is not a passing regression
// assertion of desired behavior. All service calls are synthetic stubs.
test('a late exam A response makes the exam B component save to A', async () => {
  let resolveA!: (value: any) => void;
  const delayedA = new Promise(resolve => { resolveA = resolve; });
  const exam = (id: string) => ({ id, school_id: id, class_ids: [`${id}-base`], revision: 1, status: 'draft' });
  vi.mocked(getExam).mockImplementation((id: string) => (id === 'A' ? delayedA : Promise.resolve({ exam: exam(id) })) as any);
  vi.mocked(updateExam).mockResolvedValue({ exam: exam('A') } as any);
  const page = render(<ExamStudentScopePage examId="A" canManage />);
  page.rerender(<ExamStudentScopePage examId="B" canManage />);
  await waitFor(() => expect(page.getByText('B extra')).toBeTruthy());
  await act(async () => { resolveA({ exam: exam('A') }); await delayedA; });
  await waitFor(() => expect(page.getByText('A extra')).toBeTruthy());
  fireEvent.click(page.getByText('A extra'));
  fireEvent.click(page.getByRole('button', { name: '保存范围' }));
  await waitFor(() => expect(updateExam).toHaveBeenCalledWith('A', { class_ids: ['A-base', 'A-extra'], expected_revision: 1 }));
  console.log('DEFECT REPRODUCED: rendered examId=B, late A response accepted, updateExam target=A');
});

test('selecting a class in a second grade drops selection from the first grade', async () => {
  vi.clearAllMocks();
  const current = { id: 'C', school_id: 'school', class_ids: ['g1-class'], revision: 1, status: 'draft' };
  vi.mocked(getExam).mockResolvedValue({ exam: current } as any);
  vi.mocked(updateExam).mockResolvedValue({ exam: current } as any);
  vi.mocked(listClasses).mockResolvedValue({ classes: [
    { id: 'g1-class', school_id: 'school', grade_id: 'g1', name: 'Grade one class' },
    { id: 'g2-class', school_id: 'school', grade_id: 'g2', name: 'Grade two class' },
  ] } as any);
  vi.mocked(listGrades).mockResolvedValue({ grades: [
    { id: 'g1', school_id: 'school', name: 'Grade one', academic_year: '2026' },
    { id: 'g2', school_id: 'school', name: 'Grade two', academic_year: '2026' },
  ] } as any);
  const page = render(<ExamStudentScopePage examId="C" canManage />);
  await waitFor(() => expect(page.getByText('Grade two class')).toBeTruthy());
  expect((page.getByRole('checkbox', { name: 'Grade one class' }) as HTMLInputElement).checked).toBe(true);
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 50)); });
  fireEvent.click(page.getByRole('checkbox', { name: 'Grade two class' }));
  console.log('after grade two click', {
    gradeOne: (page.getByRole('checkbox', { name: 'Grade one class' }) as HTMLInputElement).checked,
    gradeTwo: (page.getByRole('checkbox', { name: 'Grade two class' }) as HTMLInputElement).checked,
    saveDisabled: (page.getByRole('button', { name: '保存范围' }) as HTMLButtonElement).disabled,
  });
  expect((page.getByRole('checkbox', { name: 'Grade one class' }) as HTMLInputElement).checked).toBe(false);
  fireEvent.click(page.getByRole('button', { name: '保存范围' }));
  await waitFor(() => expect(updateExam).toHaveBeenCalledWith('C', { class_ids: ['g2-class'], expected_revision: 1 }));
  console.log('DEFECT REPRODUCED: selecting grade two drops previously selected grade one class');
});
