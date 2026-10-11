package answergroup

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

const DefaultAlgorithmVersion = "deterministic-complete-link-v2"

type DeterministicTextProvider struct{}

func (DeterministicTextProvider) Version() string { return "normalized-char-bigram-v2" }

func (DeterministicTextProvider) Represent(text string) Representation {
	normalized := normalizeText(text)
	features := map[string]struct{}{}
	runes := []rune(normalized)
	if len(runes) == 1 {
		features[normalized] = struct{}{}
	}
	for index := 0; index+1 < len(runes); index++ {
		features[string(runes[index:index+2])] = struct{}{}
	}
	sum := sha256.Sum256([]byte(normalized))
	return Representation{Normalized: normalized, Features: features, Hash: hex.EncodeToString(sum[:])}
}

func (DeterministicTextProvider) Similarity(left, right Representation) float64 {
	if left.Normalized == right.Normalized {
		return 1
	}
	if len(left.Features) == 0 || len(right.Features) == 0 {
		return 0
	}
	intersection := 0
	for feature := range left.Features {
		if _, ok := right.Features[feature]; ok {
			intersection++
		}
	}
	union := len(left.Features) + len(right.Features) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// 统一大小写和全角形式，但保留数字、小数、运算符等有区分意义的符号；句末句号才会被忽略。
func normalizeText(value string) string {
	value = strings.ToLower(norm.NFKC.String(strings.TrimSpace(value)))
	var builder strings.Builder
	space := false
	for _, current := range value {
		if unicode.IsSpace(current) {
			space = builder.Len() > 0
			continue
		}
		// 仅忽略句末标点。数字标点、单位、运算符和括号都可能区分不同答案。
		if current == '。' {
			continue
		}
		if space {
			builder.WriteRune(' ')
			space = false
		}
		builder.WriteRune(current)
	}
	return builder.String()
}

type representedAnswer struct {
	Source SourceAnswer
	Value  Representation
}

type groupedAnswers struct {
	Answers    []representedAnswer
	Similarity [][]float64
}

// 合并组时要求跨组的每一对答案都达到阈值，避免相似度链条把不同答案串成一组。
// 按 SegmentID 排序后，只在相似度严格更高时替换候选，平分时保持稳定结果。
func clusterAnswers(answers []SourceAnswer, provider RepresentationProvider, threshold float64) []groupedAnswers {
	represented := representAnswers(answers, provider)
	similarity := answerSimilarityMatrix(represented, provider)
	clusters := make([][]int, len(represented))
	for index := range represented {
		clusters[index] = []int{index}
	}
	for {
		bestLeft, bestRight := bestClusterPair(clusters, similarity, threshold)
		if bestLeft < 0 {
			break
		}
		clusters[bestLeft] = append(clusters[bestLeft], clusters[bestRight]...)
		clusters = append(clusters[:bestRight], clusters[bestRight+1:]...)
	}
	out := make([]groupedAnswers, 0, len(clusters))
	for _, cluster := range clusters {
		out = append(out, materializeCluster(cluster, represented, similarity))
	}
	return out
}

func representAnswers(answers []SourceAnswer, provider RepresentationProvider) []representedAnswer {
	represented := make([]representedAnswer, len(answers))
	for index, answer := range answers {
		represented[index] = representedAnswer{Source: answer, Value: provider.Represent(answer.AnswerText)}
	}
	sort.Slice(represented, func(i, j int) bool { return represented[i].Source.SegmentID < represented[j].Source.SegmentID })
	return represented
}

func answerSimilarityMatrix(represented []representedAnswer, provider RepresentationProvider) [][]float64 {
	similarity := make([][]float64, len(represented))
	for left := range represented {
		similarity[left] = make([]float64, len(represented))
		for right := range represented {
			if right < left {
				similarity[left][right] = similarity[right][left]
			} else {
				similarity[left][right] = provider.Similarity(represented[left].Value, represented[right].Value)
			}
		}
	}
	return similarity
}

func bestClusterPair(clusters [][]int, similarity [][]float64, threshold float64) (int, int) {
	bestLeft, bestRight, best := -1, -1, -1.0
	for left := 0; left < len(clusters); left++ {
		for right := left + 1; right < len(clusters); right++ {
			minimum := clusterMinimumSimilarity(clusters[left], clusters[right], similarity)
			if minimum >= threshold && minimum > best {
				bestLeft, bestRight, best = left, right, minimum
			}
		}
	}
	return bestLeft, bestRight
}

func clusterMinimumSimilarity(left, right []int, similarity [][]float64) float64 {
	minimum := 1.0
	for _, li := range left {
		for _, ri := range right {
			if similarity[li][ri] < minimum {
				minimum = similarity[li][ri]
			}
		}
	}
	return minimum
}

func materializeCluster(cluster []int, represented []representedAnswer, similarity [][]float64) groupedAnswers {
	item := groupedAnswers{Answers: make([]representedAnswer, 0, len(cluster)), Similarity: make([][]float64, len(cluster))}
	for local, global := range cluster {
		item.Answers = append(item.Answers, represented[global])
		item.Similarity[local] = make([]float64, len(cluster))
		for otherLocal, otherGlobal := range cluster {
			item.Similarity[local][otherLocal] = similarity[global][otherGlobal]
		}
	}
	return item
}

// 代表样本取组内平均相似度最高者，边界样本取最低者；单成员组必须回到人工复核。
func materializeMembers(cluster groupedAnswers, outlierThreshold float64) ([]Member, float64) {
	members := make([]Member, len(cluster.Answers))
	averages := make([]float64, len(cluster.Answers))
	for index := range cluster.Answers {
		averages[index] = memberAverageSimilarity(cluster, index)
	}
	representative, boundary := sampleMemberIndices(cluster, averages)
	homogeneity := averageHomogeneity(averages)
	for index, answer := range cluster.Answers {
		outlier := 1 - averages[index]
		members[index] = Member{
			SubmissionID: answer.Source.SubmissionID, SegmentID: answer.Source.SegmentID,
			Similarity: roundScore(averages[index]), OutlierScore: roundScore(outlier),
			Representative: index == representative, Boundary: len(cluster.Answers) > 1 && index == boundary,
			Outlier:            len(cluster.Answers) == 1 || outlier >= outlierThreshold,
			RepresentationHash: answer.Value.Hash,
		}
	}
	return members, roundScore(homogeneity)
}

func memberAverageSimilarity(cluster groupedAnswers, index int) float64 {
	if len(cluster.Answers) == 1 {
		return 0
	}
	total := 0.0
	for other, value := range cluster.Similarity[index] {
		if other != index {
			total += value
		}
	}
	return total / float64(len(cluster.Answers)-1)
}

func sampleMemberIndices(cluster groupedAnswers, averages []float64) (int, int) {
	representative, boundary := 0, 0
	for index := range averages {
		if averages[index] > averages[representative] || averages[index] == averages[representative] && cluster.Answers[index].Source.SegmentID < cluster.Answers[representative].Source.SegmentID {
			representative = index
		}
		if averages[index] < averages[boundary] || averages[index] == averages[boundary] && cluster.Answers[index].Source.SegmentID < cluster.Answers[boundary].Source.SegmentID {
			boundary = index
		}
	}
	return representative, boundary
}

func averageHomogeneity(averages []float64) float64 {
	homogeneity := 1.0
	if len(averages) == 1 {
		homogeneity = 0
	} else {
		total := 0.0
		for _, value := range averages {
			total += value
		}
		homogeneity = total / float64(len(averages))
	}
	return homogeneity
}

func roundScore(value float64) float64 { return math.Round(value*10000) / 10000 }
