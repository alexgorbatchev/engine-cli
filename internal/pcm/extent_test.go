package pcm

import (
	"fmt"
	"strings"
	"testing"
)

func extentText(rate int, packets string) string {
	return fmt.Sprintf("#software: Lavf\n#tb 0: 1/%d\n#media_type 0: audio\n#codec_id 0: pcm_f32le\n#sample_rate 0: %d\n#channel_layout_name 0: stereo\n%s", rate, rate, packets)
}

func TestExtent(t *testing.T) {
	tests := []struct {
		name, packets string
		frames        int64
		eof, reject   bool
	}{
		{"native-flush", "0, 960000, 960000, 1024, 8192, 0x139b3744\n0, 961024, 961024, 544, 4352, 0x0\n", 961568, true, false},
		{"empty-overshoot", "", 960000, false, false},
		{"frame-cap", "0, 960000, 960000, 288000, 2304000, 0x0\n", 1248000, false, false},
		{"gap-crossing-time-cap", "0, 960000, 960000, 1024, 8192, 0x0\n0, 1441024, 1441024, 1024, 8192, 0x0", 0, false, true},
		{"wrong-size", "0, 960000, 960000, 1024, 4096, 0x0", 0, false, true},
		{"negative-duration", "0, 960000, 960000, -1, -8, 0x0", 0, false, true},
		{"noninteger", "0, 960000, 960000, 1.5, 8, 0x0", 0, false, true},
		{"wrong-start", "0, 960001, 960001, 1, 8, 0x0", 0, false, true},
		{"wrong-dts", "0, 960000, 960001, 1, 8, 0x0", 0, false, true},
		{"extra-stream", "1, 960000, 960000, 1, 8, 0x0", 0, false, true},
		{"invalid-checksum", "0, 960000, 960000, 1, 8, invalid", 0, false, true},
		{"missing-field", "0, 960000, 960000, 1, 8", 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseExtent([]byte(extentText(48000, tt.packets)), 48000, 960000)
			if (err != nil) != tt.reject {
				t.Fatalf("extent=%+v error=%v", got, err)
			}
			if !tt.reject && (got.end != tt.frames || got.eof != tt.eof) {
				t.Fatalf("extent=%+v", got)
			}
		})
	}
}

func TestPacketCapIsNotEOF(t *testing.T) {
	var packets strings.Builder
	for i := range packetLimit {
		fmt.Fprintf(&packets, "0, %d, %d, 1, 8, 0x0\n", i, i)
	}
	got, err := parseExtent([]byte(extentText(44100, packets.String())), 44100, 0)
	if err != nil || got.eof || got.end != packetLimit {
		t.Fatalf("extent=%+v error=%v", got, err)
	}
	packets.WriteString("0, 256, 256, 1, 8, 0x0\n")
	if _, err := parseExtent([]byte(extentText(44100, packets.String())), 44100, 0); err == nil {
		t.Fatal("excess packet accepted")
	}
}

func TestExtentRejectsWrongFormat(t *testing.T) {
	for _, replacement := range []struct{ from, to string }{{"1/48000", "1/44100"}, {"pcm_f32le", "aac"}, {"audio", "video"}, {"stereo", "mono"}, {"#sample_rate 0: 48000", "#sample_rate 0: 44100"}, {"#codec_id 0: pcm_f32le", "#codec_id 0: pcm_f32le\n#codec_id 0: pcm_f32le"}} {
		t.Run(replacement.to, func(t *testing.T) {
			data := strings.ReplaceAll(extentText(48000, "0, 0, 0, 1, 8, 0x0\n"), replacement.from, replacement.to)
			if _, err := parseExtent([]byte(data), 48000, 0); err == nil {
				t.Fatal("incorrect format accepted")
			}
		})
	}
}
