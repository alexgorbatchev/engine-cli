#!/usr/bin/env bash

fail() { printf '%s\n' "$*" >&2; exit 1; }

sha256() {
  if command -v sha256sum >/dev/null; then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}

audio_identity() {
  cat "$@" "$script_dir/common.sh" | sha256 | awk '{print $1}'
}

prepare_audio() {
  download=''
  download_pid=''
  install=''
  keep_install=false
  trap cleanup_audio EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  mkdir -p -- "$cache"
}

cleanup_audio() {
  local status=$?
  trap - EXIT
  if [[ -n "$download_pid" ]]; then
    kill -TERM "$download_pid" 2>/dev/null || true
    wait "$download_pid" 2>/dev/null || true
  fi
  if [[ -n "$download" ]]; then rm -f -- "$download" || status=1; fi
  if [[ -n "$install" && "$keep_install" != true ]]; then rm -rf -- "$install" || status=1; fi
  exit "$status"
}

restore_audio_archive() {
  local archive=$1 url=$2 status
  if [[ "${AUDIO_CACHE_HIT:-false}" == true ]]; then
    [[ -f "$archive" && ! -L "$archive" ]] || fail "Incomplete audio cache: ${archive##*/}"
    return
  fi
  download="$(mktemp "$cache/audio.download.XXXXXX")"
  curl --fail --location --silent --show-error --retry 3 --connect-timeout 20 --max-time 180 \
    --proto '=https' --proto-redir '=https' --output "$download" "$url" &
  download_pid=$!
  if wait "$download_pid"; then
    download_pid=''
  else
    status=$?
    download_pid=''
    exit "$status"
  fi
  mv -- "$download" "$archive"
  download=''
}

verify_audio_archive() {
  local archive=$1 expected=$2 actual
  actual="$(sha256 "$archive" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || fail "Audio archive checksum mismatch: ${archive##*/}"
  printf '%s  %s\n' "$actual" "${archive##*/}"
}

require_capabilities() {
  local option=$1 listing capability
  shift
  listing="$("$audio_bin/ffmpeg$suffix" -hide_banner "$option")"
  for capability in "$@"; do
    if ! awk -v wanted="$capability" '{sub(/\r$/, ""); n = split($2, names, ","); for (i = 1; i <= n; i++) if (names[i] == wanted) found = 1} END {exit !found}' <<< "$listing"; then
      fail "Missing FFmpeg capability ($option): $capability"
    fi
  done
  printf 'Verified FFmpeg %s: %s\n' "$option" "$*"
}

verify_audio_tools() {
  local version=$1 suffix=$2 tool reported first protocols protocol
  for tool in ffmpeg ffprobe; do
    reported="$("$audio_bin/$tool$suffix" -version)"
    first="${reported%%$'\n'*}"
    case "$first" in
      "$tool version $version "*) printf '%s\n' "$first" ;;
      *) fail "Unexpected audio version: $first" ;;
    esac
  done
  require_capabilities -encoders libmp3lame aac flac alac libvorbis libopus pcm_s16be pcm_s16le ffv1
  require_capabilities -decoders pcm_f32le
  require_capabilities -muxers aiff wav mov mp4 matroska f32le
  require_capabilities -demuxers aiff wav mov matroska
  require_capabilities -devices lavfi
  require_capabilities -filters color asetpts
  protocols="$("$audio_bin/ffmpeg$suffix" -hide_banner -protocols)"
  for protocol in file pipe; do
    awk -v wanted="$protocol" '{sub(/\r$/, "")} $1 == wanted {found = 1} END {exit !found}' <<< "$protocols" || fail "Missing FFmpeg protocol: $protocol"
  done
}

publish_audio() {
  printf '%s\n' "$1" >> "${GITHUB_PATH:?}"
  keep_install=true
}
