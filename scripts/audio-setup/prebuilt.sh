#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "$0")" && pwd)"
source "$script_dir/common.sh"
[[ "$(uname -m)" == x86_64 ]] || fail 'Native Linux or Windows x64 is required.'
case "$(uname -s)" in
  Linux) platform=Linux; suffix=''; extension=tar.xz ;;
  MINGW64_NT-*) platform=Windows; suffix=.exe; extension=zip ;;
  *) fail 'Native Linux or Windows x64 is required.' ;;
esac
manifest="$script_dir/prebuilt.json"
build="$(jq --binary -er '.build' "$manifest")"
version="$(jq --binary -er '.version' "$manifest")"
asset="$(jq --binary -er --arg platform "$platform" '.assets[$platform]' "$manifest")"
url="$(jq --binary -er '.url' <<< "$asset")"
expected="$(jq --binary -er '.sha256' <<< "$asset")"
root="$(jq --binary -er '.root' <<< "$asset")"
[[ "$root" =~ ^ffmpeg-[A-Za-z0-9._-]+$ ]] || fail 'Invalid audio archive root.'
identity="$(audio_identity "$manifest" "$script_dir/prebuilt.sh")"
key="audio-btbn-$build-$platform-X64-$identity"
cache="${RUNNER_TOOL_CACHE:?}/engine-cli-audio/$key"

case "${1:-}" in
  identify)
    printf 'key=%s\npath=%s\n' "$key" "$cache" >> "${GITHUB_OUTPUT:?}"
    exit 0
    ;;
  install) ;;
  *) fail 'Usage: prebuilt.sh identify|install' ;;
esac

prepare_audio
archive="$cache/ffmpeg.$extension"
restore_audio_archive "$archive" "$url"
verify_audio_archive "$archive" "$expected"
install="$(mktemp -d "${RUNNER_TEMP:?}/engine-audio.XXXXXX")"
entries=("$root/bin/ffmpeg$suffix" "$root/bin/ffprobe$suffix")
if [[ "$platform" == Linux ]]; then
  tar --extract --xz --file "$archive" --directory "$install" --no-same-owner "${entries[@]}"
else
  unzip -q "$archive" "${entries[@]}" -d "$install"
fi
audio_bin="$install/$root/bin"
for tool in ffmpeg ffprobe; do
  binary="$audio_bin/$tool$suffix"
  [[ -f "$binary" && ! -L "$binary" && -x "$binary" ]] || fail "Audio archive must contain a regular executable: $tool"
  sha256 "$binary"
done
verify_audio_tools "$version" "$suffix"
if [[ "$platform" == Windows ]]; then
  windows_bin="$(cygpath -w "$audio_bin")"
  [[ -n "$windows_bin" ]] || fail 'Cannot determine Windows audio path.'
  publish_audio "$windows_bin"
else
  publish_audio "$audio_bin"
fi
