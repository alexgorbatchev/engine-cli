package track

import (
	"context"
	"regexp"
	"strconv"
)

const (
	transformFieldBytes = 1 << 20
	transformBatchBytes = 16 << 20
	transformIndexBytes = 8 << 20
	transformMatches    = 65536
)

func transformTooLarge() error {
	return transformError("metadata_transform_too_large", "choose fewer tracks, smaller metadata fields or a smaller replacement")
}

// expansionBound asks Go's template interpreter to count literals and references.
// Empty participating captures emit no bytes; one-byte captures emit one byte per
// reference. No replacement syntax is parsed here, including named/unknown groups.
func expansionBound(re *regexp.Regexp, template string) (int, int) {
	indices := make([]int, 2*(re.NumSubexp()+1))
	literals := len(re.ExpandString(nil, template, "x", indices))
	for i := 1; i < len(indices); i += 2 {
		indices[i] = 1
	}
	return literals, len(re.ExpandString(nil, template, "x", indices)) - literals
}

type boundedReplacement struct {
	re                   *regexp.Regexp
	template             string
	regex                bool
	literals, references int
}

func newBoundedReplacement(re *regexp.Regexp, template string, regex bool) boundedReplacement {
	r := boundedReplacement{re: re, template: template, regex: regex, literals: len(template)}
	if regex {
		r.literals, r.references = expansionBound(re, template)
	}
	return r
}

// replace retains original-source match indexes, including Go's empty match
// rules. Bounds are checked before native capture expansion can allocate.
func (r boundedReplacement) replace(ctx context.Context, source string, remaining int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(source) > transformFieldBytes {
		return "", transformTooLarge()
	}
	limit := min(transformFieldBytes, remaining)
	maxMatches := min(transformMatches, transformIndexBytes/(2*(r.re.NumSubexp()+1)*(strconv.IntSize/8))-1)
	matches := r.re.FindAllStringSubmatchIndex(source, maxMatches+1)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(matches) > maxMatches {
		return "", transformTooLarge()
	}
	result := make([]byte, 0, min(len(source), limit))
	last := 0
	for _, match := range matches {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		unchanged := match[0] - last
		if unchanged > limit-len(result) {
			return "", transformTooLarge()
		}
		result = append(result, source[last:match[0]]...)
		space := limit - len(result)
		if r.literals > space {
			return "", transformTooLarge()
		}
		maxCapture := 0
		for i := 0; i < len(match); i += 2 {
			if match[i] >= 0 {
				maxCapture = max(maxCapture, match[i+1]-match[i])
			}
		}
		if r.references > 0 && maxCapture > (space-r.literals)/r.references {
			return "", transformTooLarge()
		}
		if r.regex {
			result = r.re.ExpandString(result, r.template, source, match)
		} else {
			result = append(result, r.template...)
		}
		last = match[1]
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(source)-last > limit-len(result) {
		return "", transformTooLarge()
	}
	result = append(result, source[last:]...)
	return string(result), nil
}
