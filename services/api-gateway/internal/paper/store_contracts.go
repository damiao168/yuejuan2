package paper

var (
	_ Store                 = (*PostgresStore)(nil)
	_ Store                 = (*MemoryStore)(nil)
	_ PaperImportRepository = (*PostgresStore)(nil)
	_ PaperImportRepository = (*MemoryStore)(nil)
	_ QuestionRepository    = (*PostgresStore)(nil)
	_ QuestionRepository    = (*MemoryStore)(nil)
	_ ReadinessRepository   = (*PostgresStore)(nil)
	_ ReadinessRepository   = (*MemoryStore)(nil)
)
