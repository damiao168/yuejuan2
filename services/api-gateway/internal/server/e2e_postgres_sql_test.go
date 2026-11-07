package server

import (
	"strings"
)

func e2eSplitSQLStatements(sqlText string) []string {
	splitter := e2eSQLStatementSplitter{statements: []string{}}
	for i := 0; i < len(sqlText); {
		switch {
		case splitter.dollarQuoteTag != "":
			i = splitter.consumeDollarQuote(sqlText, i)
		case splitter.inSingleQuote:
			i = splitter.consumeSingleQuote(sqlText, i)
		default:
			i = splitter.consumeOutsideQuote(sqlText, i)
		}
	}
	splitter.finishStatement()
	return splitter.statements
}

// 迁移拆句只在引号外识别分号；保持单引号转义、dollar quote 与行注释的原始字节。
type e2eSQLStatementSplitter struct {
	statements     []string
	current        strings.Builder
	inSingleQuote  bool
	dollarQuoteTag string
}

func (s *e2eSQLStatementSplitter) consumeDollarQuote(sqlText string, i int) int {
	if strings.HasPrefix(sqlText[i:], s.dollarQuoteTag) {
		s.current.WriteString(s.dollarQuoteTag)
		i += len(s.dollarQuoteTag)
		s.dollarQuoteTag = ""
		return i
	}
	s.current.WriteByte(sqlText[i])
	return i + 1
}

func (s *e2eSQLStatementSplitter) consumeSingleQuote(sqlText string, i int) int {
	ch := sqlText[i]
	s.current.WriteByte(ch)
	if ch == '\'' {
		if i+1 < len(sqlText) && sqlText[i+1] == '\'' {
			s.current.WriteByte(sqlText[i+1])
			return i + 2
		}
		s.inSingleQuote = false
	}
	return i + 1
}

func (s *e2eSQLStatementSplitter) consumeOutsideQuote(sqlText string, i int) int {
	ch := sqlText[i]
	if ch == '\'' {
		s.inSingleQuote = true
		s.current.WriteByte(ch)
		return i + 1
	}
	if ch == '-' && i+1 < len(sqlText) && sqlText[i+1] == '-' {
		for i < len(sqlText) && sqlText[i] != '\n' {
			s.current.WriteByte(sqlText[i])
			i++
		}
		return i
	}
	if ch == '$' {
		if tag, ok := e2eDollarQuoteTag(sqlText[i:]); ok {
			s.dollarQuoteTag = tag
			s.current.WriteString(tag)
			return i + len(tag)
		}
	}
	s.current.WriteByte(ch)
	if ch == ';' && !s.inSingleQuote {
		s.finishStatement()
	}
	return i + 1
}

func (s *e2eSQLStatementSplitter) finishStatement() {
	if statement := strings.TrimSpace(s.current.String()); statement != "" {
		s.statements = append(s.statements, statement)
	}
	s.current.Reset()
}

func e2eDollarQuoteTag(sqlText string) (string, bool) {
	if sqlText == "" || sqlText[0] != '$' {
		return "", false
	}
	for i := 1; i < len(sqlText); i++ {
		if sqlText[i] == '$' {
			return sqlText[:i+1], true
		}
		if !e2eIsDollarQuoteTagChar(sqlText[i]) {
			return "", false
		}
	}
	return "", false
}

func e2eIsDollarQuoteTagChar(ch byte) bool {
	return ch == '_' ||
		(ch >= 'a' && ch <= 'z') ||
		(ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9')
}
