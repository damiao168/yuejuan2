package paper

// 组装阶段输入和来源快照的纯规则；来源绑定的数据库复验仍由阶段事务执行。

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 版本摘要只包含来源配置；识别状态或置信度的回写不应让同一批输入变成新版本。
func paperImportSourceConfigurationHash(sources []PaperImportSource) string {
	type sourceConfiguration struct {
		ID            string `json:"id"`
		FileAssetID   string `json:"file_asset_id"`
		DocumentIndex int    `json:"document_index"`
		RoleHint      string `json:"role_hint"`
	}
	ordered := append([]PaperImportSource{}, sources...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].DocumentIndex == ordered[j].DocumentIndex {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].DocumentIndex < ordered[j].DocumentIndex
	})
	configurations := make([]sourceConfiguration, 0, len(ordered))
	for _, source := range ordered {
		configurations = append(configurations, sourceConfiguration{ID: source.ID, FileAssetID: source.FileAssetID, DocumentIndex: source.DocumentIndex, RoleHint: source.RoleHint})
	}
	raw, _ := json.Marshal(configurations)
	return contentHash(raw)
}

// 当前来源在事务内完成锁定后才核对解析输入；文档必须覆盖全部来源，页面按来源和页号去重。
func validatePaperImportParseSources(current []PaperImportSource, input PaperImportParseRequest) error {
	sources := make(map[string]PaperImportSource, len(current))
	for _, source := range current {
		sources[source.ID] = source
	}
	seen := make(map[string]bool, len(input.Documents))
	for _, document := range input.Documents {
		source, ok := sources[document.SourceID]
		if !ok || seen[document.SourceID] || source.FileAssetID != document.FileAssetID || source.DocumentIndex != document.DocumentIndex || strings.TrimSpace(document.Content) == "" {
			return ErrInvalidInput
		}
		seen[document.SourceID] = true
	}
	if len(seen) != len(sources) {
		return ErrInvalidInput
	}
	seenPages := make(map[string]bool, len(input.Pages))
	for _, page := range input.Pages {
		source, ok := sources[page.SourceID]
		pageKey := fmt.Sprintf("%s:%d", page.SourceID, page.PageNo)
		if !ok || source.DocumentIndex != page.DocumentIndex || page.PageNo <= 0 || strings.TrimSpace(page.FileAssetID) == "" || seenPages[pageKey] {
			return ErrInvalidInput
		}
		if digest := strings.ToLower(strings.TrimSpace(page.SHA256)); digest != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(digest) {
			return ErrInvalidInput
		}
		seenPages[pageKey] = true
	}
	return nil
}

func buildDecodedVisualParseInput(job PaperImportJob, baseDocuments []PaperImportParseDocument, pages []PaperImportDecodedPage) (PaperImportParseRequest, error) {
	if len(job.Sources) == 0 || len(pages) == 0 {
		return PaperImportParseRequest{}, ErrInvalidInput
	}
	sources := make(map[string]PaperImportSource, len(job.Sources))
	for _, source := range job.Sources {
		sources[source.ID] = source
	}
	baseBySource, err := indexDecodedBaseDocuments(sources, baseDocuments)
	if err != nil {
		return PaperImportParseRequest{}, err
	}
	orderedPages, pagesBySource, err := orderDecodedVisualPages(sources, pages)
	if err != nil {
		return PaperImportParseRequest{}, err
	}
	orderedSources := append([]PaperImportSource(nil), job.Sources...)
	sort.SliceStable(orderedSources, func(i, j int) bool { return orderedSources[i].DocumentIndex < orderedSources[j].DocumentIndex })
	documents := make([]PaperImportParseDocument, 0, len(orderedSources))
	for _, source := range orderedSources {
		if pagesBySource[source.ID] > 0 {
			documents = append(documents, visualParseDocument(source.ID, source.FileAssetID, source.DocumentIndex, source.RoleHint))
			continue
		}
		document, ok := baseBySource[source.ID]
		if !ok {
			return PaperImportParseRequest{}, ErrInvalidInput
		}
		documents = append(documents, document)
	}
	return PaperImportParseRequest{Documents: documents, Pages: orderedPages}, nil
}

func indexDecodedBaseDocuments(sources map[string]PaperImportSource, documents []PaperImportParseDocument) (map[string]PaperImportParseDocument, error) {
	indexed := make(map[string]PaperImportParseDocument, len(documents))
	for _, document := range documents {
		source, ok := sources[document.SourceID]
		if !ok || source.FileAssetID != document.FileAssetID || source.DocumentIndex != document.DocumentIndex || strings.TrimSpace(document.Content) == "" {
			return nil, ErrInvalidInput
		}
		if _, duplicate := indexed[document.SourceID]; duplicate {
			return nil, ErrInvalidInput
		}
		indexed[document.SourceID] = document
	}
	return indexed, nil
}

func orderDecodedVisualPages(sources map[string]PaperImportSource, pages []PaperImportDecodedPage) ([]PaperImportDecodedPage, map[string]int, error) {
	counts := make(map[string]int, len(pages))
	seen := make(map[string]bool, len(pages))
	// 先复制再稳定排序，避免完成回执中原有页面顺序被组装步骤改动。
	ordered := append([]PaperImportDecodedPage(nil), pages...)
	for _, page := range ordered {
		source, ok := sources[page.SourceID]
		identity := fmt.Sprintf("%s:%d", page.SourceID, page.PageNo)
		if !ok || source.DocumentIndex != page.DocumentIndex || page.PageNo <= 0 || strings.TrimSpace(page.FileAssetID) == "" || seen[identity] {
			return nil, nil, ErrInvalidInput
		}
		seen[identity] = true
		counts[page.SourceID]++
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].DocumentIndex == ordered[j].DocumentIndex {
			return ordered[i].PageNo < ordered[j].PageNo
		}
		return ordered[i].DocumentIndex < ordered[j].DocumentIndex
	})
	return ordered, counts, nil
}

func paperImportNewSourceIDs(documents []PaperImportParseDocument, snapshot []byte) ([]string, error) {
	var previous []PaperImportSource
	if json.Unmarshal(snapshot, &previous) != nil {
		return nil, ErrInvalidInput
	}
	previousIDs := make(map[string]bool, len(previous))
	for _, source := range previous {
		previousIDs[source.ID] = true
	}
	var added []string
	for _, document := range documents {
		if !previousIDs[document.SourceID] {
			added = append(added, document.SourceID)
		}
	}
	return added, nil
}

func mapFromJSON(value any) map[string]any {
	raw, _ := json.Marshal(value)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
