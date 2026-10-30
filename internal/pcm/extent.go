package pcm

import (
	"fmt"
	"strconv"
	"strings"
)

type extent struct {
	end     int64
	packets int
	eof     bool
}

// FFmpeg's framecrc muxer supplies timestamps/durations of the resampled PCM,
// including decoder discard and resampler flush. Checksums are not used to claim
// stochastic codec realizations from separate seeks are byte-identical.
func parseExtent(data []byte, rate int, start int64) (extent, error) {
	result := extent{end: start}
	headers := map[string]string{}
	var frames int64
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.HasPrefix(line, "#") {
			key, value, ok := strings.Cut(line, ":")
			if !ok || headers[key] != "" {
				return extent{}, ErrTimeline
			}
			headers[key] = strings.TrimSpace(value)
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		duration, err := parsePacket(line, result.end)
		if err != nil {
			return extent{}, err
		}
		result.end += duration
		frames += duration
		result.packets++
		if result.packets > packetLimit || frames > int64(probeSeconds*rate) {
			return extent{}, ErrTimeline
		}
	}
	if headers["#tb 0"] != fmt.Sprintf("1/%d", rate) || headers["#media_type 0"] != "audio" || headers["#codec_id 0"] != "pcm_f32le" || headers["#sample_rate 0"] != strconv.Itoa(rate) || headers["#channel_layout_name 0"] != "stereo" {
		return extent{}, ErrTimeline
	}
	// Output caps are not EOF. Empty overshot seeks are inconclusive as well.
	result.eof = result.packets > 0 && result.packets < packetLimit && frames < int64(probeSeconds*rate)
	return result, nil
}

func parsePacket(line string, expected int64) (int64, error) {
	fields := strings.Split(line, ",")
	if len(fields) != 6 {
		return 0, ErrTimeline
	}
	values := make([]int64, 5)
	for i := range values {
		n, err := parseInteger(fields[i])
		if err != nil {
			return 0, ErrTimeline
		}
		values[i] = n
	}
	if values[0] != 0 || values[1] != expected || values[2] != expected || values[3] <= 0 || values[3] > probeSeconds*48000 || values[4] != values[3]*BytesPerFrame {
		return 0, ErrTimeline
	}
	if _, err := strconv.ParseUint(strings.TrimSpace(fields[5]), 0, 32); err != nil {
		return 0, ErrTimeline
	}
	return values[3], nil
}
