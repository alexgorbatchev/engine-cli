package fingerprint

import (
	"context"
	"testing"
)

func evidence(seed uint32, n int) Audio {
	a := Audio{Profile: Profile, Duration: float64(n)*0.1238 + 2.6, Raw: make([]uint32, n)}
	for i := range a.Raw {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		a.Raw[i] = seed
	}
	return a
}

func TestCompare(t *testing.T) {
	a := evidence(123, 1000)
	if _, err := Compare(context.Background(), evidence(1, 524287), evidence(2, 524287)); err == nil {
		t.Fatal("accepted fingerprints beyond matcher limits")
	}
	if m, err := Compare(context.Background(), Audio{Profile: Profile}, a); err != nil || m.Duplicate {
		t.Fatalf("empty audio: %v %v", m, err)
	}
	for _, tt := range []struct {
		name      string
		b         Audio
		duplicate bool
	}{
		{"same", a, true}, {"unrelated", evidence(456, 1000), false},
		{"silence", Audio{Profile: Profile, Duration: a.Duration, Raw: make([]uint32, 1000)}, false},
		{"short excerpt", Audio{Profile: Profile, Duration: 20, Raw: a.Raw[:100]}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Compare(context.Background(), a, tt.b)
			if err != nil {
				t.Fatal(err)
			}
			if m.Duplicate != tt.duplicate {
				t.Fatalf("match: %+v", m)
			}
		})
	}
	b := evidence(123, 1000)
	for i := 400; i < 500; i++ {
		b.Raw[i] ^= 0xffff
	}
	m, err := Compare(context.Background(), a, b)
	if err != nil || m.Duplicate {
		t.Fatalf("global average concealed a different section: %+v %v", m, err)
	}
	b = evidence(123, 1000)
	for i := 400; i < 600; i++ {
		b.Raw[i] = ^b.Raw[i]
	}
	m, err = Compare(context.Background(), a, b)
	if err != nil || m.Duplicate {
		t.Fatalf("matching sections must not conceal an unmatched middle: %+v %v", m, err)
	}
	b.Profile = "other"
	if _, err := Compare(context.Background(), a, b); err == nil {
		t.Fatal("accepted incompatible profiles")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compare(ctx, a, a); err == nil {
		t.Fatal("ignored cancellation")
	}
}
