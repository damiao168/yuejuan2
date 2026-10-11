package paper

import (
	"reflect"
	"testing"
)

func TestBatch25VisualParseDocumentsKeepTextAndSourceOrder(t *testing.T) {
	text := normalizedImportDocument{SourceID: "text", Content: "已有文本", DocumentIndex: 7, RoleHint: "questions"}
	assets := []PaperImportOCRAsset{
		{SourceID: "image-second", FileAssetID: "asset-2", DocumentIndex: 2, RoleHint: "answers"},
		{SourceID: "image-first", FileAssetID: "asset-1", DocumentIndex: 1, RoleHint: "questions"},
	}
	documents := appendVisualParseDocuments([]normalizedImportDocument{text}, assets)
	if len(documents) != 3 || !reflect.DeepEqual(documents[0], text) {
		t.Fatalf("existing text changed: %#v", documents)
	}
	for i, asset := range assets {
		got := documents[i+1]
		if got.SourceID != asset.SourceID || got.FileAssetID != asset.FileAssetID || got.DocumentIndex != asset.DocumentIndex || got.RoleHint != asset.RoleHint || got.Content != directVisualDocumentPlaceholder || got.Blocks == nil || len(got.Blocks) != 0 {
			t.Fatalf("visual source order/metadata changed: %#v", got)
		}
	}
	if got := appendVisualParseDocuments([]normalizedImportDocument{text}, nil); !reflect.DeepEqual(got, []normalizedImportDocument{text}) {
		t.Fatalf("no visual sources changed text: %#v", got)
	}
}
