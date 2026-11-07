-- 试改样本只来自同一道题。先通过租户和题目索引限定范围，再展开该题的样本，
-- 避免每次正式评分都扫描同租户其他考试、题目的全部历史试改；不改写既有成绩或试改记录。
CREATE INDEX idx_scoring_ai_trial_question ON scoring_ai_trial(tenant_id, question_id);

CREATE OR REPLACE FUNCTION mark_scoring_trial_sample_grade() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM scoring_ai_trial trial CROSS JOIN LATERAL jsonb_array_elements(trial.payload->'samples') sample
   WHERE trial.tenant_id=NEW.tenant_id AND trial.question_id=NEW.question_id
     AND sample->>'segment_id'=NEW.answer_segment_id::text) THEN
   NEW.risk_flags=COALESCE(NEW.risk_flags,'[]'::jsonb);
   IF NOT NEW.risk_flags @> '["trial_sample"]'::jsonb THEN NEW.risk_flags=NEW.risk_flags || '["trial_sample"]'::jsonb; END IF;
   NEW.needs_human_review=true;
 END IF;
 RETURN NEW;
END $$;
