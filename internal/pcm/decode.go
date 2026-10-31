package pcm

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// run always waits for its direct child, including after cancellation or a bounded
// reader failure. No shell, pathname protocol, or caller-supplied FFmpeg arguments.
func (s *Store) run(ctx context.Context, g *generation, args []string, limit int) ([]byte, error) {
	if _, err := g.file.Seek(0, io.SeekStart); err != nil {
		return nil, ErrChanged
	}
	cmd := exec.CommandContext(ctx, s.program, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin = g.file
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("PCM output: %w", err)
	}
	cmd.Cancel = func() error {
		// A launcher descendant can retain stdout after the direct child exits.
		// Cancel the reader as well as the producer before joining the child.
		_ = stdout.Close()
		return cmd.Process.Kill()
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(stdout, int64(limit)+1))
	if len(data) > limit {
		readErr = ErrTimeline
	}
	if readErr != nil {
		_ = stdout.Close()
		_ = cmd.Process.Kill() /* Wait below joins the producer on every path. */
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, waitErr)
	}
	return data, nil
}

func decoderArgs(aligned int64) []string {
	args := []string{"-hide_banner", "-v", "error", "-nostdin", "-threads", "1", "-xerror", "-copyts", "-start_at_zero"}
	// Even seeking to zero changes native AAC skip handling. The head must open
	// without a seek so the container's presentation/priming metadata is honored.
	if aligned > 0 {
		args = append(args, "-ss", strconv.FormatInt(aligned-1, 10), "-noaccurate_seek")
	}
	return append(args, "-protocol_whitelist", "fd", "-blocksize", "65536", "-i", "fd:", "-map", "0:a:0", "-vn", "-sn", "-dn")
}

func resampleFilter(rate int, aligned int64) string {
	filter := fmt.Sprintf("aresample=%d:resampler=swr:filter_size=32:phase_shift=10:linear_interp=1:exact_rational=1:dither_method=none:out_sample_fmt=flt:out_chlayout=stereo", rate)
	// Do not trim sample zero before resampling: native skip/discard and edit-list
	// metadata determine priming. Nonzero anchors include a fixed native warmup.
	if aligned > 0 {
		filter = fmt.Sprintf("atrim=start=%d,", aligned) + filter
	}
	return filter
}

func (s *Store) probe(ctx context.Context, g *generation, aligned int64) (extent, error) {
	rate := g.metadata.SampleRate
	filter := fmt.Sprintf("%s,atrim=end_sample=%d", resampleFilter(rate, aligned), probeSeconds*rate)
	args := append(decoderArgs(aligned), "-af", filter, "-c:a", "pcm_f32le", "-enc_time_base:a", fmt.Sprintf("1:%d", rate), "-avoid_negative_ts", "disabled", "-frames:a", strconv.Itoa(packetLimit), "-f", "framecrc", "pipe:1")
	data, err := s.run(ctx, g, args, metadataLimit)
	if err != nil {
		return extent{}, err
	}
	return parseExtent(data, rate, aligned*int64(rate))
}

func (s *Store) decodePage(ctx context.Context, g *generation, start int64) ([]byte, error) {
	rate := g.metadata.SampleRate
	aligned := max(0, start/int64(rate)-PageSeconds)
	measured, err := s.probe(ctx, g, aligned)
	if err != nil {
		return nil, err
	}
	if measured.packets == 0 {
		return nil, ErrExtentUnknown
	}
	if measured.eof {
		if err := establishEnd(g, measured.end); err != nil {
			return nil, err
		}
	}
	end := min(start+PageSeconds*int64(rate), measured.end)
	if start >= measured.end {
		if measured.eof {
			return []byte{}, nil
		}
		return nil, ErrExtentUnknown
	}
	if end < start+PageSeconds*int64(rate) && !measured.eof {
		return nil, ErrTimeline
	}
	first := start - aligned*int64(rate)
	filter := fmt.Sprintf("%s,atrim=start_sample=%d:end_sample=%d", resampleFilter(rate, aligned), first, first+end-start)
	args := append(decoderArgs(aligned), "-af", filter, "-c:a", "pcm_f32le", "-enc_time_base:a", fmt.Sprintf("1:%d", rate), "-avoid_negative_ts", "disabled", "-f", "f32le", "pipe:1")
	data, err := s.run(ctx, g, args, int(end-start)*BytesPerFrame)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != (end-start)*BytesPerFrame {
		return nil, ErrTimeline
	}
	return data, nil
}

func establishEnd(g *generation, end int64) error {
	if end < g.provenFrames {
		return ErrTimeline
	}
	if g.metadata.LengthFrames != nil && *g.metadata.LengthFrames != end {
		return ErrTimeline
	}
	g.metadata.LengthFrames = &end
	return nil
}

func parseInteger(raw string) (int64, error) { return strconv.ParseInt(strings.TrimSpace(raw), 10, 64) }
