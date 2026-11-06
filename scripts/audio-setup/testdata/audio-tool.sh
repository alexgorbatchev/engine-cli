#!/bin/bash
set -euo pipefail
tool=${0##*/}
printf 'execute %s %s\n' "$tool" "$*" >> "$AUDIO_TEST_EVENTS"
tool=${tool%.exe}
if [ "${AUDIO_TEST_PLATFORM:-macOS}" = macOS ]; then
  for candidate in ffmpeg ffprobe; do
    test -f "$AUDIO_TEST_STATE/signed-$candidate"
    test -f "$AUDIO_TEST_STATE/linked-$candidate"
  done
fi
emit() {
  if [ "$2" != "${AUDIO_TEST_OMIT:-}" ]; then printf ' %s %s Test capability\n' "$1" "$2"; fi
}
listing() {
case " $* " in
  *' -version '*)
    version=$AUDIO_TEST_VERSION
    if [ "$tool" = ffprobe ]; then version=${AUDIO_TEST_PROBE_VERSION:-$version}; fi
    printf '%s version %s Copyright test fixture\n' "$tool" "$version" ;;
  *' -encoders '*)
    for codec in libmp3lame aac flac alac libvorbis libopus pcm_s16be pcm_s16le ffv1; do
      emit A..... "$codec"
    done ;;
  *' -decoders '*) emit A..... pcm_f32le ;;
  *' -muxers '*) for format in aiff wav mov mp4 matroska f32le; do emit E "$format"; done ;;
  *' -demuxers '*)
    for format in aiff wav mov,mp4,m4a,3gp,3g2,mj2 matroska,webm f32le; do emit D "$format"; done
    printf ' D d lavfi Test input device\n' ;;
  *' -devices '*) emit D lavfi ;;
  *' -protocols '*)
    printf 'Input:\n'
    for protocol in file pipe; do
      if [ "$protocol" != "${AUDIO_TEST_OMIT:-}" ]; then printf '  %s\n' "$protocol"; fi
    done ;;
  *' -filters '*) for filter in color asetpts; do emit ... "$filter"; done ;;
  *) printf 'Unexpected tool invocation: %s\n' "$*" >&2; exit 90 ;;
esac
}
if [ "${AUDIO_TEST_CRLF:-false}" = true ]; then
  listing "$@" | awk '{printf "%s\r\n", $0}'
else
  listing "$@"
fi
