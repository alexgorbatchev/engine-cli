package volumes

import (
	"context"
	"errors"
	"testing"
)

func TestReportListsAttachedDrivesFirstWithFreeSpace(t *testing.T) {
	free := func(path string) (uint64, error) {
		if path == "/Volumes/GONE" {
			return 0, errors.New("detached")
		}
		return 600, nil
	}
	got, err := report(context.Background(), []mount{{path: "/", name: "Macintosh HD"}, {path: "/Volumes/DJ_USB/", removable: true}, {path: "/Volumes/GONE", removable: true}}, free)
	if err != nil {
		t.Fatal(err)
	}
	names := ""
	for _, v := range got.Volumes {
		names += v.Name + ","
	}
	if names != "DJ_USB,GONE,Macintosh HD," || got.Volumes[0].Path != "/Volumes/DJ_USB" || *got.Volumes[0].FreeBytes != 600 || got.Volumes[1].FreeBytes != nil || got.Volumes[2].Removable {
		t.Fatalf("volumes=%+v", got.Volumes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := report(ctx, []mount{{path: "/"}}, free); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list: %v", err)
	}
}

func TestListReadsThisComputer(t *testing.T) {
	got, err := List(context.Background())
	if err != nil || len(got.Volumes) == 0 {
		t.Fatalf("list=%+v err=%v", got, err)
	}
}
