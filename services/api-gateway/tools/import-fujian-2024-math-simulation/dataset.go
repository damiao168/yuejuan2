// 输入定义和载入仅用于匿名仿真数据；官方题目保留来源，考生与识别结果必须标记为合成。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	examName    = "2024福建中考数学真实流程仿真（Q1-Q19）"
	datasetID   = "fujian-2024-junior-math-v1"
	imageWidth  = 1654
	imageHeight = 2339
	cropPadding = 24
)

type questionDef struct {
	QuestionNo     string  `json:"question_no"`
	QuestionType   string  `json:"question_type"`
	Score          float64 `json:"score"`
	Stem           string  `json:"stem"`
	StandardAnswer any     `json:"standard_answer"`
}

type candidateAnswer struct {
	Text             any      `json:"text"`
	Confidence       float64  `json:"confidence"`
	Route            string   `json:"route"`
	ExpectedScore    *float64 `json:"expected_score"`
	NeedsHumanReview bool     `json:"needs_human_review"`
	Synthetic        bool     `json:"synthetic"`
}

type candidateDef struct {
	CandidateNo string                     `json:"candidate_no"`
	Profile     string                     `json:"profile"`
	Answers     map[string]candidateAnswer `json:"answers"`
	PageFiles   []string                   `json:"page_files"`
	Synthetic   bool                       `json:"synthetic"`
}

type region struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type manifest struct {
	Synthetic             bool           `json:"synthetic"`
	SimulationOnly        bool           `json:"simulation_only"`
	TrainingAllowed       bool           `json:"training_allowed"`
	ContainsRealStudentID bool           `json:"contains_real_student_identity"`
	Files                 map[string]any `json:"files"`
}

type dataset struct {
	Manifest   manifest
	Questions  []questionDef
	Candidates []candidateDef
	Regions    map[string]map[string]region
}

func loadDataset(dir string) (dataset, error) {
	var data dataset
	if err := readJSON(filepath.Join(dir, "manifest.json"), &data.Manifest); err != nil {
		return data, err
	}
	if err := readJSON(filepath.Join(dir, "questions.json"), &data.Questions); err != nil {
		return data, err
	}
	if err := readJSON(filepath.Join(dir, "candidates.json"), &data.Candidates); err != nil {
		return data, err
	}
	if err := readJSON(filepath.Join(dir, "regions.json"), &data.Regions); err != nil {
		return data, err
	}
	if len(data.Questions) != 19 || len(data.Candidates) < 2 {
		return data, errors.New("expected 19 questions and multiple candidates")
	}
	for _, candidate := range data.Candidates {
		if !candidate.Synthetic {
			return data, fmt.Errorf("candidate %s is not marked synthetic", candidate.CandidateNo)
		}
		if len(candidate.PageFiles) != 2 {
			return data, fmt.Errorf("candidate %s does not have two pages", candidate.CandidateNo)
		}
	}
	return data, nil
}

func fileFromManifest(datasetDir string, files map[string]any, key string) string {
	value, _ := files[key].(string)
	return filepath.Join(datasetDir, filepath.FromSlash(value))
}

func readJSON(path string, target any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, target)
}

func questionNumber(questionNo string) int {
	value, _ := strconv.Atoi(strings.TrimPrefix(questionNo, "Q"))
	return value
}

func displayText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, fmt.Sprint(item))
		}
		return strings.Join(items, ",")
	case []string:
		return strings.Join(typed, ",")
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
