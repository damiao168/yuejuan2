-- 失败调用也保留白名单诊断；NULL 表示旧记录未采集，不能解释为零调用或零用量。
-- 只扩展现有运行表，读取仍由 Store 显式限定租户；沿用归档屏障和 worker 原子结算，不回填历史猜测值。
ALTER TABLE subjective_grading_run ADD COLUMN execution_diagnostics JSONB;
ALTER TABLE subjective_grading_run ADD CONSTRAINT subjective_run_execution_diagnostics_shape
  CHECK (execution_diagnostics IS NULL OR
    (jsonb_typeof(execution_diagnostics)='object' AND octet_length(execution_diagnostics::text)<=8192));
