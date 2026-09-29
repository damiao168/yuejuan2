-- Authored question content must not be stored in bank_content: that column is
-- reserved for immutable question-bank snapshots and rejects manual questions.
ALTER TABLE question ADD COLUMN options JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE question ADD COLUMN parent_question_no TEXT NOT NULL DEFAULT '';
ALTER TABLE question ADD COLUMN subquestion_no TEXT NOT NULL DEFAULT '';
ALTER TABLE question ADD CONSTRAINT question_options_array CHECK (jsonb_typeof(options) = 'array');
