#!/usr/bin/env bash
set -euo pipefail

fail() { printf '%s\n' "$*" >&2; exit 1; }

[[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || fail 'Native macOS ARM64 is required.'
script_dir="$(cd -- "$(dirname -- "$0")" && pwd)"
manifest="$script_dir/macos.json"
build="$(jq -er '.build' "$manifest")"
version="$(jq -er '.version' "$manifest")"
team="$(jq -er '.teamID' "$manifest")"
identity="$(cat "$manifest" "$script_dir/macos.sh" | shasum -a 256 | awk '{print $1}')"
key="macos-audio-martin-riedl-$build-ARM64-$identity"
cache="${RUNNER_TOOL_CACHE:?}/engine-cli-audio/$key"

case "${1:-}" in
  identify)
    printf 'key=%s\npath=%s\n' "$key" "$cache" >> "${GITHUB_OUTPUT:?}"
    exit 0
    ;;
  install) ;;
  *) fail 'Usage: macos.sh identify|install' ;;
esac

download=''
download_pid=''
install=''
keep_install=false
cleanup() {
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
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p -- "$cache"
for tool in ffmpeg ffprobe; do
  archive="$cache/$tool.zip"
  if [[ "${MACOS_AUDIO_CACHE_HIT:-false}" == true ]]; then
    [[ -f "$archive" && ! -L "$archive" ]] || fail "Incomplete audio cache: $tool.zip"
  else
    url="$(jq -er --arg tool "$tool" '.assets[$tool].url' "$manifest")"
    download="$(mktemp "$cache/$tool.download.XXXXXX")"
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
  fi
done

# Verify the complete pair before extracting or executing either binary.
for tool in ffmpeg ffprobe; do
  expected="$(jq -er --arg tool "$tool" '.assets[$tool].sha256' "$manifest")"
  actual="$(shasum -a 256 "$cache/$tool.zip" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || fail "Audio archive checksum mismatch: $tool.zip"
  printf '%s  %s.zip\n' "$actual" "$tool"
  [[ "$(unzip -Z -1 "$cache/$tool.zip")" == "$tool" ]] || fail "Unexpected audio archive entries: $tool.zip"
done

install="$(mktemp -d "${RUNNER_TEMP:?}/engine-audio.XXXXXX")"
for tool in ffmpeg ffprobe; do
  unzip -q "$cache/$tool.zip" -d "$install"
  binary="$install/$tool"
  [[ -f "$binary" && ! -L "$binary" && -x "$binary" ]] || fail "Audio archive must contain a regular executable: $tool"
  [[ "$(lipo -archs "$binary")" == arm64 ]] || fail "Non-native audio binary: $tool"
  codesign --verify --strict --verbose=2 \
    -R="anchor apple generic and certificate leaf[subject.OU] = \"$team\"" "$binary"
  codesign --display --verbose=4 "$binary"
  libraries="$(otool -L "$binary")"
  printf '%s\n' "$libraries"
  while IFS= read -r library; do
    case "$library" in
      /System/Library/Frameworks/*|/usr/lib/*) ;;
      *) fail "Non-system audio dependency: $library" ;;
    esac
  done <<< "$(printf '%s\n' "$libraries" | awk 'NR > 1 {print $1}')"
  shasum -a 256 "$binary"
done

for tool in ffmpeg ffprobe; do
  reported="$("$install/$tool" -version)"
  first="${reported%%$'\n'*}"
  case "$first" in
    "$tool version $version "*) printf '%s\n' "$first" ;;
    *) fail "Unexpected audio version: $first" ;;
  esac
done

require_capabilities() {
  local option=$1 listing capability
  shift
  listing="$("$install/ffmpeg" -hide_banner "$option")"
  for capability in "$@"; do
    if ! awk -v wanted="$capability" '{n = split($2, names, ","); for (i = 1; i <= n; i++) if (names[i] == wanted) found = 1} END {exit !found}' <<< "$listing"; then
      fail "Missing FFmpeg capability ($option): $capability"
    fi
  done
  printf 'Verified FFmpeg %s: %s\n' "$option" "$*"
}
require_capabilities -encoders libmp3lame aac flac alac libvorbis libopus pcm_s16be pcm_s16le ffv1
require_capabilities -decoders pcm_f32le
require_capabilities -muxers aiff wav mov mp4 matroska f32le
require_capabilities -demuxers lavfi aiff wav mov matroska
require_capabilities -filters color asetpts
protocols="$("$install/ffmpeg" -hide_banner -protocols)"
for protocol in file pipe; do
  awk -v wanted="$protocol" '$1 == wanted {found = 1} END {exit !found}' <<< "$protocols" || fail "Missing FFmpeg protocol: $protocol"
done

printf '%s\n' "$install" >> "${GITHUB_PATH:?}"
keep_install=true
