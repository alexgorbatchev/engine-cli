package fingerprint

import (
	"context"
	"fmt"
	"math"
	"math/bits"

	"github.com/alexgorbatchev/gochromaprint"
)

// Conservative candidate thresholds are evidence filters, not probabilities.
const minimumCoverage = 0.98
const maximumMeanBitErrors = 2.0
const maximumItemBitErrors = 5
const minimumDistinctWords = 16

type Match struct {
	Duplicate     bool    `json:"duplicate_candidate"`
	CoverageA     float64 `json:"coverage_a"`
	CoverageB     float64 `json:"coverage_b"`
	AgreementA    float64 `json:"agreement_coverage_a"`
	AgreementB    float64 `json:"agreement_coverage_b"`
	MeanBitErrors float64 `json:"mean_bit_errors"`
	OffsetSeconds float64 `json:"offset_seconds"`
	Reason        string  `json:"reason"`
}

// Compare uses Chromaprint alignment but verifies the entire aligned overlap.
// Its matcher can merge separated matching segments; segment duration alone
// therefore cannot establish that two complete recordings match.
func Compare(ctx context.Context, a, b Audio) (Match, error) {
	if err := ctx.Err(); err != nil {
		return Match{}, err
	}
	if a.Profile != Profile || b.Profile != Profile {
		return Match{}, fmt.Errorf("incompatible fingerprint profiles")
	}
	if !informative(a) || !informative(b) {
		return Match{Reason: "insufficient_audio_evidence"}, nil
	}
	if math.Min(a.Duration, b.Duration)/math.Max(a.Duration, b.Duration) < minimumCoverage {
		return Match{Reason: "different_duration"}, nil
	}
	c := gochromaprint.New(gochromaprint.AlgorithmDefault)
	m := gochromaprint.NewMatcher(c.Config())
	segments, err := m.MatchContext(ctx, a.Raw, b.Raw)
	if err != nil {
		return Match{}, err
	}
	if err := ctx.Err(); err != nil {
		return Match{}, err
	}
	if len(segments) == 0 {
		return Match{Reason: "no_alignment"}, nil
	}
	offset := segments[0].Pos1 - segments[0].Pos2
	x, y := max(0, offset), max(0, -offset)
	n := min(len(a.Raw)-x, len(b.Raw)-y)
	result := Match{CoverageA: float64(n) / float64(len(a.Raw)), CoverageB: float64(n) / float64(len(b.Raw)), OffsetSeconds: m.HashTime(offset), Reason: "different_audio"}
	agree := 0
	for i := range n {
		errors := bits.OnesCount32(a.Raw[x+i] ^ b.Raw[y+i])
		result.MeanBitErrors += float64(errors)
		if errors <= maximumItemBitErrors {
			agree++
		}
	}
	result.MeanBitErrors /= float64(n)
	result.AgreementA = float64(agree) / float64(len(a.Raw))
	result.AgreementB = float64(agree) / float64(len(b.Raw))
	result.Duplicate = result.AgreementA >= minimumCoverage && result.AgreementB >= minimumCoverage && result.MeanBitErrors <= maximumMeanBitErrors
	if result.Duplicate {
		result.Reason = "near_full_audio_match"
	}
	return result, nil
}

func informative(a Audio) bool {
	if a.Duration <= 0 || math.IsNaN(a.Duration) || math.IsInf(a.Duration, 0) {
		return false
	}
	words := make(map[uint32]struct{})
	for _, word := range a.Raw {
		words[word] = struct{}{}
		if len(words) >= minimumDistinctWords {
			return true
		}
	}
	return false
}
