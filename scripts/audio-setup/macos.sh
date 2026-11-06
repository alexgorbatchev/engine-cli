#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "$0")" && pwd)"
source "$script_dir/common.sh"
[[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || fail 'Native macOS ARM64 is required.'
manifest="$script_dir/macos.json"
build="$(jq -er '.build' "$manifest")"
version="$(jq -er '.version' "$manifest")"
team="$(jq -er '.teamID' "$manifest")"
identity="$(audio_identity "$manifest" "$script_dir/macos.sh")"
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

prepare_audio
for tool in ffmpeg ffprobe; do
  archive="$cache/$tool.zip"
  url="$(jq -er --arg tool "$tool" '.assets[$tool].url' "$manifest")"
  restore_audio_archive "$archive" "$url"
done

# Verify the complete pair before extracting or executing either binary.
for tool in ffmpeg ffprobe; do
  expected="$(jq -er --arg tool "$tool" '.assets[$tool].sha256' "$manifest")"
  verify_audio_archive "$cache/$tool.zip" "$expected"
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
  sha256 "$binary"
done
audio_bin=$install
verify_audio_tools "$version" ''
publish_audio "$audio_bin"
