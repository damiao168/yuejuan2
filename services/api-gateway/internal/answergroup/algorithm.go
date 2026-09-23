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

func normalizeText(value string) string {
	value = strings.ToLower(norm.NFKC.String(strings.TrimSpace(value)))
	var builder strings.Builder
	space := false
	for _, current := range value {
		if unicode.IsSpace(current) {
			space = builder.Len() > 0
			continue
		}
		// Only the sentence terminator is ignored. Numeric punctuation, units,
		// operators and brackets all distinguish potentially different answers.
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

// clusterAnswers uses deterministic complete-link agglomeration. Complete-link
// is intentionally conservative: every pair in a merged group must meet the
// threshold, avoiding chain-shaped mixed groups in the first pilot.
func clusterAnswers(answers []SourceAnswer, provider RepresentationProvider, threshold float64) []groupedAnswers {
	represented := make([]representedAnswer, len(answers))
	for index, answer := range answers {
		represented[index] = representedAnswer{Source: answer, Value: provider.Represent(answer.AnswerText)}
	}
	sort.Slice(represented, func(i, j int) bool { return represented[i].Source.SegmentID < represented[j].Source.SegmentID })
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
	clusters := make([][]int, len(represented))
	for index := range represented {
		clusters[index] = []int{index}
	}
	for {
		bestLeft, bestRight, best := -1, -1, -1.0
		for left := 0; left < len(clusters); left++ {
			for right := left + 1; right < len(clusters); right++ {
				minimum := 1.0
				for _, li := range clusters[left] {
					for _, ri := range clusters[right] {
						if similarity[li][ri] < minimum {
							minimum = similarity[li][ri]
						}
					}
				}
				if minimum >= threshold && minimum > best {
					bestLeft, bestRight, best = left, right, minimum
				}
			}
		}
		if bestLeft < 0 {
			break
		}
		clusters[bestLeft] = append(clusters[bestLeft], clusters[bestRight]...)
		clusters = append(clusters[:bestRight], clusters[bestRight+1:]...)
	}
	out := make([]groupedAnswers, 0, len(clusters))
	for _, cluster := range clusters {
		item := groupedAnswers{Answers: make([]representedAnswer, 0, len(cluster)), Similarity: make([][]float64, len(cluster))}
		for local, global := range cluster {
			item.Answers = append(item.Answers, represented[global])
			item.Similarity[local] = make([]float64, len(cluster))
			for otherLocal, otherGlobal := range cluster {
				item.Similarity[local][otherLocal] = similarity[global][otherGlobal]
			}
		}
		out = append(out, item)
	}
	return out
}

func materializeMembers(cluster groupedAnswers, outlierThreshold float64) ([]Member, float64) {
	members := make([]Member, len(cluster.Answers))
	averages := make([]float64, len(cluster.Answers))
	for index := range cluster.Answers {
		if len(cluster.Answers) == 1 {
			averages[index] = 0
		} else {
			total := 0.0
			for other, value := range cluster.Similarity[index] {
				if other != index {
					total += value
				}
			}
			averages[index] = total / float64(len(cluster.Answers)-1)
		}
	}
	representative, boundary := 0, 0
	for index := range averages {
		if averages[index] > averages[representative] || averages[index] == averages[representative] && cluster.Answers[index].Source.SegmentID < cluster.Answers[representative].Source.SegmentID {
			representative = index
		}
		if averages[index] < averages[boundary] || averages[index] == averages[boundary] && cluster.Answers[index].Source.SegmentID < cluster.Answers[boundary].Source.SegmentID {
			boundary = index
		}
	}
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

func roundScore(value float64) float64 { return math.Round(value*10000) / 10000 }
