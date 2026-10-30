package track

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/alexgorbatchev/engine-cli/internal/progress"
)

func TestBoundedReplacementNativeSemantics(t *testing.T) {
	for _, tc := range []struct{ name, pattern, source, template string }{
		{"anchors", "(?m)^|$", "first\nsecond", "${0}x"},
		{"empty adjacency", "a*", "aa世界a", "[${0}]"},
		{"Unicode", "(?i)(ä)(.)", "Äther äpfel", "$2${1}$$"},
		{"optional unknown", "(?P<a>x)?(y)", "y xy", "$a:${2}:$unknown:${99}"},
		{"duplicate names", "(?P<a>x)|(?P<a>y)", "xy", "${a}${a}"},
		{"malformed dollars", "(x)", "xx", "$ $- ${ $$ $1x ${1}x"},
		{"large literals", "x", "x", strings.Repeat("$", 65536)},
		{"no match", "z", "abc", "$0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			re := regexp.MustCompile(tc.pattern)
			r := newBoundedReplacement(re, tc.template, true)
			got, err := r.replace(t.Context(), tc.source, transformBatchBytes)
			want := re.ReplaceAllString(tc.source, tc.template)
			if err != nil || got != want {
				t.Fatalf("native semantics: got %q want %q err=%v", got, want, err)
			}
		})
	}
}

func TestBoundedReplacementBudgets(t *testing.T) {
	for _, tc := range []struct {
		name, pattern, source, template string
		regex                           bool
		remaining                       int
	}{
		{"source", "x", strings.Repeat("x", transformFieldBytes+1), "", false, transformBatchBytes},
		{"match count", "x", strings.Repeat("x", transformMatches+1), "", false, transformBatchBytes},
		{"capture index memory", strings.Repeat("()", 1024), strings.Repeat("x", 1024), "", true, transformBatchBytes},
		{"unmatched prefix", "x", "longprefixx", "", false, 1},
		{"replacement", "x", "x", "long", false, 1},
		{"single capture", "^(.*)$", strings.Repeat("x", 2048), strings.Repeat("$1", 1024), true, transformBatchBytes},
		{"unmatched tail", "x", "xtail", "", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newBoundedReplacement(regexp.MustCompile(tc.pattern), tc.template, tc.regex)
			if _, err := r.replace(t.Context(), tc.source, tc.remaining); progress.ErrorCode(err) != "metadata_transform_too_large" {
				t.Fatalf("accepted excessive work: %v", err)
			}
		})
	}
}

// cancelAtCheck cancels a real context at a deterministic ownership checkpoint.
// It permits native matching to finish, then cancels during result assembly.
type cancelAtCheck struct {
	context.Context
	cancel context.CancelFunc
	calls  int
	at     int
}

func (c *cancelAtCheck) Err() error {
	c.calls++
	if c.calls == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestBoundedReplacementCancellation(t *testing.T) {
	for _, at := range []int{1, 2, 4, 6, 7} {
		ctx, cancel := context.WithCancel(t.Context())
		c := &cancelAtCheck{Context: ctx, cancel: cancel, at: at}
		r := newBoundedReplacement(regexp.MustCompile("x"), "y", false)
		_, err := r.replace(c, "xxxx", transformBatchBytes)
		cancel()
		if !errors.Is(err, context.Canceled) || c.calls != at {
			t.Fatalf("checkpoint %d: calls=%d err=%v", at, c.calls, err)
		}
	}
}
