package paper

import "encoding/json"

func wireList[T any](items []T) []T { return append([]T{}, items...) }

// MarshalJSON keeps the public array contract even for historical/parser
// records with absent collections. Copy before normalization: serialization
// must never mutate the store's candidates or immutable input/result facts.
func (job PaperImportJob) MarshalJSON() ([]byte, error) {
	type wireJob PaperImportJob
	job.Sources = wireList(job.Sources)
	job.Issues = wireList(job.Issues)
	job.QuestionCandidates = wireList(job.QuestionCandidates)
	for i := range job.QuestionCandidates {
		q := &job.QuestionCandidates[i]
		q.Options = wireList(q.Options)
		q.KnowledgePointHints = wireList(q.KnowledgePointHints)
		q.SourceRefs = wireList(q.SourceRefs)
		q.Issues = wireList(q.Issues)
	}
	job.AnswerCandidates = wireList(job.AnswerCandidates)
	for i := range job.AnswerCandidates {
		a := &job.AnswerCandidates[i]
		a.EquivalentAnswers = wireList(a.EquivalentAnswers)
		a.SourceRefs = wireList(a.SourceRefs)
		a.Issues = wireList(a.Issues)
	}
	job.SolutionCandidates = wireList(job.SolutionCandidates)
	for i := range job.SolutionCandidates {
		s := &job.SolutionCandidates[i]
		s.Steps = wireList(s.Steps)
		s.SourceRefs = wireList(s.SourceRefs)
		s.Issues = wireList(s.Issues)
	}
	job.RubricCandidates = wireList(job.RubricCandidates)
	for i := range job.RubricCandidates {
		r := &job.RubricCandidates[i]
		r.Points = wireList(r.Points)
		r.Deductions = wireList(r.Deductions)
		r.Examples = wireList(r.Examples)
		r.SourceRefs = wireList(r.SourceRefs)
		r.Issues = wireList(r.Issues)
	}
	job.StructuredIssues = wireList(job.StructuredIssues)
	for i := range job.StructuredIssues {
		job.StructuredIssues[i].SourceRefs = wireList(job.StructuredIssues[i].SourceRefs)
	}
	job.Questions = wireList(job.Questions)
	for i := range job.Questions {
		q := &job.Questions[i]
		q.SourceRefs = wireList(q.SourceRefs)
		q.Options = wireList(q.Options)
		q.KnowledgePoints = wireList(q.KnowledgePoints)
		q.Issues = wireList(q.Issues)
		if q.AnswerKey != nil {
			answer := *q.AnswerKey
			answer.EquivalentAnswers = wireList(answer.EquivalentAnswers)
			q.AnswerKey = &answer
		}
		if q.Solution != nil {
			solution := *q.Solution
			solution.Steps = wireList(solution.Steps)
			solution.SourceRefs = wireList(solution.SourceRefs)
			q.Solution = &solution
		}
	}
	return json.Marshal(wireJob(job))
}
