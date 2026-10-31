package staging

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalCopyNativeIORefusesClosedDescriptors(t *testing.T) {
	for _, name := range []string{"read", "write", "cancelled", "source observation"} {
		t.Run(name, func(t *testing.T) {
			source := localSourceFixture(t)
			in, err := os.Open(source.Path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { in.Close() })
			out, err := os.Create(filepath.Join(t.TempDir(), "private-copy"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { out.Close() })
			ctx := t.Context()
			want := os.ErrClosed
			switch name {
			case "read", "source observation":
				if err := in.Close(); err != nil {
					t.Fatal(err)
				}
			case "write":
				if err := out.Close(); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx, want = cancelled, context.Canceled
			}
			if name == "source observation" {
				err = source.check(in)
			} else {
				err = copyLocalBytes(ctx, out, in)
			}
			if !errors.Is(err, want) {
				t.Fatalf("native I/O refusal lost: got %v want %v", err, want)
			}
			if info, err := os.Stat(source.Path); err != nil || info.Size() != source.Size {
				t.Fatalf("I/O refusal changed source: %+v %v", info, err)
			}
		})
	}
}
