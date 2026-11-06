#!/bin/bash
set -eu
tool=${0##*/}
printf 'execute %s %s\n' "$tool" "$*" >> "$AUDIO_TEST_EVENTS"
for candidate in ffmpeg ffprobe; do
  test -f "$AUDIO_TEST_STATE/signed-$candidate"
  test -f "$AUDIO_TEST_STATE/linked-$candidate"
done
case " $* " in
  *' -version '*) printf '%s version %s Copyright test fixture\n' "$tool" "$AUDIO_TEST_VERSION" ;;
  *' -encoders '*)
    for codec in libmp3lame aac flac alac libvorbis libopus pcm_s16be pcm_s16le ffv1; do
      if [ "$codec" != "${AUDIO_TEST_OMIT:-}" ]; then printf ' A..... %s Test encoder\n' "$codec"; fi
    done ;;
  *' -decoders '*) printf ' A..... pcm_f32le Test decoder\n' ;;
  *' -formats '*) printf ' DE aiff\n DE wav\n DE mov,mp4,m4a,3gp,3g2,mj2\n DE matroska,webm\n DE f32le\n D lavfi\n' ;;
  *' -muxers '*) printf ' E aiff\n E wav\n E mov\n E mp4\n E matroska\n E f32le\n' ;;
  *' -demuxers '*) printf ' D aiff\n D wav\n D mov,mp4,m4a,3gp,3g2,mj2\n D matroska,webm\n D lavfi\n D f32le\n' ;;
  *' -protocols '*) printf 'Input:\n  file\n  pipe\nOutput:\n  file\n  pipe\n' ;;
  *' -filters '*) printf ' ... color |->V Test color\n ... asetpts A->A Test timestamp filter\n' ;;
  *) printf 'Unexpected tool invocation: %s\n' "$*" >&2; exit 90 ;;
esac
