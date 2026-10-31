package performance

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/alexgorbatchev/engine-cli/internal/audiodecode"
	"github.com/alexgorbatchev/engine-cli/internal/fileidentity"
	"github.com/nateranda/djtools/engine"
)

func writeAudio(t *testing.T, path string) {
	t.Helper()
	const rate = 48000
	const seconds = 2
	const sampleBytes = 2
	pcm := make([]byte, rate*seconds*sampleBytes)
	var wav bytes.Buffer
	wav.WriteString("RIFF")
	if err := binary.Write(&wav, binary.LittleEndian, uint32(len(pcm)+36)); err != nil {
		t.Fatal(err)
	}
	wav.WriteString("WAVEfmt ")
	for _, value := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * sampleBytes), uint16(sampleBytes), uint16(16)} {
		if err := binary.Write(&wav, binary.LittleEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	wav.WriteString("data")
	if err := binary.Write(&wav, binary.LittleEndian, uint32(len(pcm))); err != nil {
		t.Fatal(err)
	}
	wav.Write(pcm)
	if err := os.WriteFile(path, wav.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAudioFallbackInitializesExactNativeExtent(t *testing.T) {
	d, root := fixture(t)
	writeAudio(t, filepath.Join(root, "song.wav"))
	p := prepare(t, d, root, Edit{Slot: 1, Position: 1.125, Name: "Native audio", Color: "#12AB34"})
	result, err := p.Apply(context.Background(), d)
	if err != nil || !result.Changed {
		t.Fatalf("set=%+v err=%v", result, err)
	}
	saved := inspect(t, d, root)
	if saved.Revision != result.Revision || len(saved.Cues) != 1 || saved.Cues[0].Position != 1.125 {
		t.Fatalf("saved=%+v result=%+v", saved, result)
	}
	current, err := load(context.Background(), d, root, 1)
	if err != nil {
		t.Fatal(err)
	}
	beat, err := engine.DecodeBeatData(current.Beat)
	if err != nil || beat.SampleRate != 48000 || beat.SampleLimit != 96000 {
		t.Fatalf("stored native extent=%+v err=%v", beat, err)
	}
	if len(current.Track) != 0 || len(current.Loops) != 0 {
		t.Fatal("fallback initialization rewrote unrelated native data")
	}
	if _, err := os.Stat(filepath.Join(root, "song.wav")); err != nil {
		t.Fatal(err)
	}
}

func TestMediaFallbackRevisionBindsInspectionAndApply(t *testing.T) {
	for _, change := range []string{"replacement", "removed", "modified"} {
		t.Run(change, func(t *testing.T) {
			d, root := fixture(t)
			path := filepath.Join(root, "song.wav")
			writeAudio(t, path)
			edit := Edit{Identifier: "1", Slot: 1, Position: 1, Name: "Reviewed", Color: "#ABCDEF", Revision: inspect(t, d, root).Revision}
			p, err := Prepare(context.Background(), d, root, edit)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "replacement":
				if err := os.Rename(path, path+".old"); err != nil {
					t.Fatal(err)
				}
				writeAudio(t, path)
			case "removed":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "modified":
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				modified := info.ModTime().Add(time.Second)
				if err := os.Chtimes(path, modified, modified); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Prepare(context.Background(), d, root, edit); code(err) != "cue_revision_stale" {
				t.Fatalf("old inspection accepted changed media: %v", err)
			}
			if _, err := p.Apply(context.Background(), d); code(err) != "cue_revision_stale" {
				t.Fatalf("prepared edit accepted changed media: %v", err)
			}
			current, err := load(context.Background(), d, root, 1)
			if err != nil || len(current.Cues) != 0 || len(current.Beat) != 0 {
				t.Fatalf("rejected edit changed native data: %+v %v", current, err)
			}
		})
	}
}

func TestMediaIdentityRejectsReplacementWithIdenticalMetadata(t *testing.T) {
	d, root := fixture(t)
	path := filepath.Join(root, "song.wav")
	writeAudio(t, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	before := inspect(t, d, root)
	if again := inspect(t, d, root); again.Revision != before.Revision {
		t.Fatal("unchanged file changed its inspected identity")
	}
	edit := Edit{Identifier: "1", Slot: 1, Position: 1, Name: "Reviewed", Color: "#ABCDEF", Revision: before.Revision}
	p, err := Prepare(t.Context(), d, root, edit)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	writeAudio(t, path)
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	replacement, err := os.Stat(path)
	if err != nil || replacement.Size() != info.Size() || !replacement.ModTime().Equal(info.ModTime()) {
		t.Fatalf("replacement must preserve size and exact mtime: %v", err)
	}
	after := inspect(t, d, root)
	if after.Revision == before.Revision {
		t.Fatal("different file retained the old inspected identity despite identical metadata")
	}
	if _, err := Prepare(t.Context(), d, root, edit); code(err) != "cue_revision_stale" {
		t.Fatalf("old inspection accepted replacement: %v", err)
	}
	if _, err := p.Apply(t.Context(), d); code(err) != "cue_revision_stale" {
		t.Fatalf("old prepared write accepted replacement: %v", err)
	}
	edit.Revision = after.Revision
	fresh, err := Prepare(t.Context(), d, root, edit)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := fresh.Apply(t.Context(), d); err != nil || !result.Changed {
		t.Fatalf("fresh review could not save replacement: %+v %v", result, err)
	}
}

func TestNonregularMediaCannotInitializeCues(t *testing.T) {
	d, root := fixture(t)
	if err := os.Mkdir(filepath.Join(root, "song.wav"), 0700); err != nil {
		t.Fatal(err)
	}
	before := inspect(t, d, root)
	if _, err := Prepare(t.Context(), d, root, Edit{Identifier: "1", Slot: 1, Position: 0, Color: "#FFFFFF", Revision: before.Revision}); code(err) != "cue_media_unavailable" {
		t.Fatalf("directory accepted as audio: %v", err)
	}
	if after := inspect(t, d, root); after.Revision != before.Revision || len(after.Cues) != 0 {
		t.Fatalf("rejected media changed saved cues: %+v", after)
	}
}

func TestUnobservableMediaCannotProduceReusableRevision(t *testing.T) {
	d, root := fixture(t)
	before := inspect(t, d, root)
	if _, err := d.Exec("UPDATE Track SET path=? WHERE id=1", "invalid\x00.wav"); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(t.Context(), d, root, "1"); code(err) != "cue_media_unavailable" {
		t.Fatalf("unobservable media produced an inspected revision: %v", err)
	}
	if _, err := Prepare(t.Context(), d, root, Edit{Identifier: "1", Slot: 1, Revision: before.Revision, Position: 1, Color: "#FFFFFF"}); code(err) != "cue_media_unavailable" {
		t.Fatalf("unobservable media accepted a saved cue edit: %v", err)
	}
	var beat, cues []byte
	if err := d.QueryRow("SELECT beatData,quickCues FROM PerformanceData WHERE trackId=1").Scan(&beat, &cues); err != nil || len(beat) != 0 || len(cues) != 0 {
		t.Fatalf("unobservable edit changed native performance: beat=%x cues=%x err=%v", beat, cues, err)
	}
}

func TestCapturedMediaDetectsChangesBeforePublication(t *testing.T) {
	for _, change := range []string{"removed", "replacement", "modified"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "song.wav")
			writeAudio(t, path)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := fileidentity.Identity(path, info)
			if err != nil {
				t.Fatalf("capturing native media identity: %v", err)
			}
			captured := &mediaSource{path: path, info: info, identity: identity}
			if err := captured.check(); err != nil {
				t.Fatalf("unchanged media rejected: %v", err)
			}
			switch change {
			case "removed":
				err = os.Remove(path)
			case "replacement":
				err = os.Rename(path, path+".old")
				if err == nil {
					writeAudio(t, path)
				}
			case "modified":
				modified := info.ModTime().Add(time.Second)
				err = os.Chtimes(path, modified, modified)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := captured.check(); code(err) != "cue_media_changed" {
				t.Fatalf("captured source accepted %s: %v", change, err)
			}
		})
	}
}

func TestNativeExtentDoesNotDependOnAudioAvailability(t *testing.T) {
	d, root := fixture(t)
	if _, err := d.Exec("UPDATE PerformanceData SET trackData=? WHERE trackId=1", nativeExtentBlob(t)); err != nil {
		t.Fatal(err)
	}
	before := inspect(t, d, root)
	writeAudio(t, filepath.Join(root, "song.wav"))
	if after := inspect(t, d, root); after.Revision != before.Revision {
		t.Fatal("native extent unexpectedly depends on media availability")
	}
	p, err := Prepare(context.Background(), d, root, Edit{Identifier: "1", Slot: 8, Position: 9.5, Name: "Offline native", Color: "#FFFFFF", Revision: before.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "song.wav")); err != nil {
		t.Fatal(err)
	}
	if result, err := p.Apply(context.Background(), d); err != nil || !result.Changed {
		t.Fatalf("native extent required online audio: %+v %v", result, err)
	}
}

func TestMissingPerformanceRowIsCreatedFromRealAudio(t *testing.T) {
	d, root := fixture(t)
	if _, err := d.Exec("DELETE FROM PerformanceData WHERE trackId=1"); err != nil {
		t.Fatal(err)
	}
	writeAudio(t, filepath.Join(root, "song.wav"))
	before := inspect(t, d, root)
	if len(before.Cues) != 0 || len(before.Loops) != 0 {
		t.Fatalf("absent row invented saved markers: %+v", before)
	}
	result, err := prepare(t, d, root, Edit{Slot: 8, Position: 0.75, Name: "Created row", Color: "#112233"}).Apply(t.Context(), d)
	if err != nil || !result.Changed {
		t.Fatalf("set missing row=%+v error=%v", result, err)
	}
	after := inspect(t, d, root)
	if after.Revision != result.Revision || len(after.Cues) != 1 || after.Cues[0].Slot != 8 || after.Cues[0].Position != 0.75 {
		t.Fatalf("saved=%+v result=%+v", after, result)
	}
}

func TestFallbackCountsFirstAudioInsteadOfLongerVideo(t *testing.T) {
	d, root := fixture(t)
	writeAudio(t, filepath.Join(root, "short.wav"))
	path := filepath.Join(root, "song.mkv")
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-nostdin", "-i", filepath.Join(root, "short.wav"), "-f", "lavfi", "-i", "color=c=black:s=16x16:r=1:d=5", "-map", "0:a:0", "-map", "1:v:0", "-c:a", "pcm_s16le", "-c:v", "ffv1", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("creating mismatched audio/video fixture: %v: %s", err, output)
	}
	out, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "json", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var container struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &container); err != nil {
		t.Fatalf("fixture container extent=%s err=%v", out, err)
	}
	duration, err := strconv.ParseFloat(container.Format.Duration, 64)
	if err != nil || duration <= 3 {
		t.Fatalf("fixture must extend beyond the rejected cue: duration=%v err=%v", duration, err)
	}
	if _, err := d.Exec("UPDATE Track SET path='song.mkv' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	before := inspect(t, d, root)
	edit := Edit{Identifier: "1", Slot: 1, Position: 3, Color: "#FFFFFF", Revision: before.Revision}
	if _, err := Prepare(t.Context(), d, root, edit); code(err) != "cue_invalid" {
		t.Fatalf("accepted cue beyond the actual first audio stream: %v", err)
	}
	if result := inspect(t, d, root); result.Revision != before.Revision || len(result.Cues) != 0 {
		t.Fatalf("rejected cue changed native data: %+v", result)
	}
	edit.Position = 1.75
	p, err := Prepare(t.Context(), d, root, edit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	current, err := load(t.Context(), d, root, 1)
	if err != nil {
		t.Fatal(err)
	}
	beat, err := engine.DecodeBeatData(current.Beat)
	if err != nil || beat.SampleRate != 48000 || beat.SampleLimit != 96000 {
		t.Fatalf("used container or estimator extent: %+v err=%v", beat, err)
	}
}

func TestNativeDecoderCancellationAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio.wav")
	writeAudio(t, path)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var frames int
	err := audiodecode.Stream(ctx, path, 48000, func(samples []float32) error {
		frames += len(samples)
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || frames == 0 {
		t.Fatalf("decoder cancellation err=%v frames=%d", err, frames)
	}
	if err := os.Truncate(path, 45); err != nil {
		t.Fatal(err)
	}
	if rate, err := audiodecode.NativeSampleRate(t.Context(), path); err != nil || rate != 48000 {
		t.Fatalf("corrupt fixture must retain a probeable rate: %v %v", rate, err)
	}
	if _, _, err := probeExtent(t.Context(), path); code(err) != "cue_media_unavailable" {
		t.Fatalf("decoder failure lost stable cue error: %v", err)
	}
}
