package transfer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nateranda/djtools/engine"
	"github.com/nateranda/djtools/lib"
)

func TestSyncPreservesNativeHotCueColors(t *testing.T) {
	source, s := library(t, "source")
	destination, d := library(t, "destination")
	execSQL(t, s, "INSERT INTO Track(title,path,filename) VALUES('Cues','song.mp3','song.mp3')")
	if err := os.WriteFile(filepath.Join(source, "song.mp3"), []byte("audio"), 0600); err != nil {
		t.Fatal(err)
	}
	beat, err := engine.EncodeUnsetBeatData(48000, 480000)
	if err != nil {
		t.Fatal(err)
	}
	colors := []string{"#123ABC", "#000000", "#FFFFFF", "#12ABEF", "#F4D338", "#998877", "#010203", "#FEDCBA"}
	var cues []byte
	for i, color := range colors {
		cues, err = engine.SetSavedHotCue(beat, cues, lib.HotCue{Position: i + 1, Offset: float64(i), Name: fmt.Sprintf("Cue %d", i+1), Color: color})
		if err != nil {
			t.Fatal(err)
		}
	}
	execSQL(t, s, "UPDATE PerformanceData SET beatData=?,quickCues=? WHERE trackId=1", beat, cues)
	opts := Options{Source: source, Destination: destination, Scope: Scope{Root: true}, DryRun: true, NoBackup: true}
	preview, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.DryRun = false
	opts.PlanToken = preview.PlanToken
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	var savedBeat, savedCues []byte
	if err := d.QueryRow("SELECT beatData,quickCues FROM PerformanceData WHERE trackId=1").Scan(&savedBeat, &savedCues); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beat, savedBeat) || !bytes.Equal(cues, savedCues) {
		t.Fatal("sync changed native performance bytes")
	}
	saved, err := engine.DecodeSavedPerformance(savedBeat, savedCues, nil)
	if err != nil || len(saved.HotCues) != len(colors) {
		t.Fatalf("synced cues: %+v %v", saved.HotCues, err)
	}
	for i, cue := range saved.HotCues {
		if cue.Position != i+1 || cue.Color != colors[i] || cue.Offset != float64(i) {
			t.Fatalf("synced cue %d: %+v", i+1, cue)
		}
	}
}
