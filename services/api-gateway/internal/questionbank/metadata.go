package questionbank

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

var metadataKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var reservedMetadataKeys = map[string]bool{
	"subject_code": true, "education_stage": true, "grade_scope": true,
	"question_type": true, "assessment_archetype": true, "knowledge_points": true,
	"difficulty_band": true, "cognitive_level": true, "suggested_time_minutes": true,
	"source_year": true, "copyright": true, "language": true, "intended_use": true,
}

func (e *MetadataValidationError) Unwrap() error { return ErrInvalidInput }

func copyMetadataValues(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	raw, _ := json.Marshal(values)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func metadataError(field, message string) *MetadataValidationError {
	return &MetadataValidationError{FieldErrors: map[string][]string{field: {message}}}
}

func normalizeSchema(fields []MetadataFieldDefinition, taxonomies []TaxonomyDefinition) ([]MetadataFieldDefinition, []TaxonomyDefinition, error) {
	fields = append([]MetadataFieldDefinition{}, fields...)
	taxonomies = append([]TaxonomyDefinition{}, taxonomies...)
	if len(fields) > 64 || len(taxonomies) > 32 {
		return nil, nil, ErrInvalidInput
	}
	taxonomyByID := map[string]TaxonomyDefinition{}
	for ti := range taxonomies {
		t := &taxonomies[ti]
		t.ID, t.Label = strings.TrimSpace(t.ID), strings.TrimSpace(t.Label)
		if !metadataKeyPattern.MatchString(t.ID) || t.Label == "" || !validText(t.Label, 160) || len(t.Terms) == 0 || len(t.Terms) > 1000 {
			return nil, nil, metadataError(fmt.Sprintf("taxonomies.%d", ti), "taxonomy id, label, and terms are required")
		}
		if _, exists := taxonomyByID[t.ID]; exists {
			return nil, nil, metadataError("taxonomies."+t.ID, "taxonomy id must be unique")
		}
		seen := map[string]bool{}
		for i := range t.Terms {
			term := &t.Terms[i]
			term.ID, term.Label = strings.TrimSpace(term.ID), strings.TrimSpace(term.Label)
			if !metadataKeyPattern.MatchString(term.ID) || term.Label == "" || !validText(term.Label, 160) || seen[term.ID] {
				return nil, nil, metadataError("taxonomies."+t.ID, "term ids must be unique stable identifiers")
			}
			seen[term.ID] = true
		}
		taxonomyByID[t.ID] = *t
	}
	seenFields := map[string]bool{}
	for fi := range fields {
		f := &fields[fi]
		f.Key, f.Label, f.Type, f.TaxonomyID = strings.TrimSpace(f.Key), strings.TrimSpace(f.Label), strings.TrimSpace(f.Type), strings.TrimSpace(f.TaxonomyID)
		path := "fields." + f.Key
		if !metadataKeyPattern.MatchString(f.Key) || reservedMetadataKeys[f.Key] || f.Label == "" || !validText(f.Label, 160) || seenFields[f.Key] || !oneOf(f.Type, "enum", "string", "number", "boolean", "taxonomy") {
			return nil, nil, metadataError(path, "field key, label, and supported type are required")
		}
		seenFields[f.Key] = true
		switch f.Type {
		case "string":
			if f.MaxLength == nil || *f.MaxLength < 1 || *f.MaxLength > 2000 {
				return nil, nil, metadataError(path, "string max_length must be between 1 and 2000")
			}
		case "number":
			if f.Min != nil && (math.IsNaN(*f.Min) || math.IsInf(*f.Min, 0)) || f.Max != nil && (math.IsNaN(*f.Max) || math.IsInf(*f.Max, 0)) || f.Min != nil && f.Max != nil && *f.Min > *f.Max {
				return nil, nil, metadataError(path, "number range is invalid")
			}
		case "enum":
			if len(f.Options) == 0 || len(f.Options) > 256 {
				return nil, nil, metadataError(path, "enum options are required")
			}
			seenOptions := map[string]bool{}
			for oi := range f.Options {
				o := &f.Options[oi]
				o.Value, o.Label = strings.TrimSpace(o.Value), strings.TrimSpace(o.Label)
				if !metadataKeyPattern.MatchString(o.Value) || o.Label == "" || !validText(o.Label, 160) || seenOptions[o.Value] {
					return nil, nil, metadataError(path, "enum options need unique stable values and labels")
				}
				seenOptions[o.Value] = true
			}
		case "taxonomy":
			if _, ok := taxonomyByID[f.TaxonomyID]; !ok {
				return nil, nil, metadataError(path, "taxonomy_id must reference a taxonomy in this schema")
			}
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	sort.Slice(taxonomies, func(i, j int) bool { return taxonomies[i].ID < taxonomies[j].ID })
	return fields, taxonomies, nil
}

func numberValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func validateMetadataValues(schema MetadataSchema, values map[string]any, requireRequired bool) MetadataValidationResult {
	// 按题目绑定的 schema 校验，未知字段一律报错；调用方决定本次是否要求补齐必填项。
	result := MetadataValidationResult{Valid: true, FieldErrors: map[string][]string{}}
	definitions := map[string]MetadataFieldDefinition{}
	for _, f := range schema.Fields {
		definitions[f.Key] = f
	}
	for key := range values {
		if _, ok := definitions[key]; !ok {
			result.FieldErrors["custom_metadata."+key] = []string{"field is not declared by the bound metadata schema"}
		}
	}
	taxonomies := map[string]TaxonomyDefinition{}
	for _, t := range schema.Taxonomies {
		taxonomies[t.ID] = t
	}
	for _, f := range schema.Fields {
		value, present := values[f.Key]
		if !present || value == nil || value == "" {
			if requireRequired && f.Required {
				result.FieldErrors["custom_metadata."+f.Key] = []string{"value is required"}
			}
			continue
		}
		path := "custom_metadata." + f.Key
		switch f.Type {
		case "string":
			text, ok := value.(string)
			if !ok || f.MaxLength == nil || !validText(text, *f.MaxLength) {
				result.FieldErrors[path] = []string{"value must be a bounded string"}
			}
		case "number":
			n, ok := numberValue(value)
			if !ok || f.Min != nil && n < *f.Min || f.Max != nil && n > *f.Max {
				result.FieldErrors[path] = []string{"value must be a number within the declared range"}
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				result.FieldErrors[path] = []string{"value must be a boolean"}
			}
		case "enum":
			text, ok := value.(string)
			valid := false
			for _, option := range f.Options {
				valid = valid || option.Active && option.Value == text
			}
			if !ok || !valid {
				result.FieldErrors[path] = []string{"value must be an active declared option"}
			}
		case "taxonomy":
			text, ok := value.(string)
			valid := false
			for _, term := range taxonomies[f.TaxonomyID].Terms {
				valid = valid || term.Active && term.ID == text
			}
			if !ok || !valid {
				result.FieldErrors[path] = []string{"value must be an active stable taxonomy id"}
			}
		}
	}
	result.Valid = len(result.FieldErrors) == 0
	return result
}

func validationErr(result MetadataValidationResult) error {
	if result.Valid {
		return nil
	}
	return &MetadataValidationError{FieldErrors: result.FieldErrors}
}

func validateContentMetadata(schema MetadataSchema, content Content, requireRequired bool) MetadataValidationResult {
	result := validateMetadataValues(schema, content.CustomMetadata, requireRequired)
	var controlled *TaxonomyDefinition
	for i := range schema.Taxonomies {
		if schema.Taxonomies[i].ID == "knowledge_points" {
			controlled = &schema.Taxonomies[i]
			break
		}
	}
	if controlled != nil {
		// 配置受控知识点后，存稳定 ID 而非显示名称；停用词条不能用于通过本次校验。
		active := map[string]bool{}
		for _, term := range controlled.Terms {
			active[term.ID] = term.Active
		}
		for _, point := range content.KnowledgePoints {
			if !active[point] {
				result.FieldErrors["knowledge_points"] = append(result.FieldErrors["knowledge_points"], "knowledge point must be an active stable taxonomy id: "+point)
			}
		}
	}
	result.Valid = len(result.FieldErrors) == 0
	return result
}
