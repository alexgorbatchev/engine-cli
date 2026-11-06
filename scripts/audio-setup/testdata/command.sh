#!/bin/bash
set -eu
name=${0##*/}
case "$name" in
  uname)
    case "$1" in
      -s) printf '%s\n' "${AUDIO_TEST_OS:-Darwin}" ;;
      -m) printf '%s\n' "${AUDIO_TEST_ARCH:-arm64}" ;;
      *) exit 90 ;;
    esac
    ;;
  curl)
    output= url=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --output|-o) output=$2; shift 2 ;;
        --proto|--proto-redir|--connect-timeout|--max-time|--retry|--retry-delay) shift 2 ;;
        --fail|--location|--silent|--show-error|--retry-all-errors|-f|-L|-s|-S|-fsSL|-fSL) shift ;;
        https://audio.example/*.zip) url=$1; shift ;;
        *) printf 'Unexpected curl argument: %s\n' "$1" >&2; exit 90 ;;
      esac
    done
    [ -n "$output" ] && [ -n "$url" ]
    printf 'download %s\n' "${url##*/}" >> "$AUDIO_TEST_EVENTS"
    if [ "${AUDIO_TEST_DOWNLOAD_FAIL:-}" = "${url##*/}" ]; then
      printf 'partial download' > "$output"
      exit 22
    fi
    if [ "${AUDIO_TEST_DOWNLOAD_BLOCK:-}" = "${url##*/}" ]; then
      printf 'partial download' > "$output"
      printf '%s\n' "$$" > "$AUDIO_TEST_STATE/downloader.pid"
      exec sleep 60
    fi
    cat "$AUDIO_TEST_DOWNLOADS/${url##*/}" > "$output"
    ;;
  unzip)
    printf 'unzip %s\n' "$*" >> "$AUDIO_TEST_EVENTS"
    exec "$AUDIO_TEST_UNZIP" "$@"
    ;;
  lipo|codesign|otool)
    target=${!#}
    tool=${target##*/}
    printf '%s %s %s\n' "$name" "$tool" "$*" >> "$AUDIO_TEST_EVENTS"
    if [ "${AUDIO_TEST_NATIVE_FAIL:-}" = "$name:$tool" ]; then exit 23; fi
    case "$name" in
      lipo)
        printf '%s\n' "${AUDIO_TEST_BINARY_ARCH:-arm64}"
        ;;
      codesign)
        if [[ " $* " == *' --verify '* ]]; then
          [[ " $* " == *' --strict '* ]]
          [[ " $* " == *' TESTTEAM01 '* || " $* " == *'"TESTTEAM01"'* ]]
          touch "$AUDIO_TEST_STATE/signed-$tool"
        else
          printf 'Authority=Developer ID Application: Test Fixture (TESTTEAM01)\nTeamIdentifier=TESTTEAM01\n' >&2
        fi
        ;;
      otool)
        printf '%s:\n\t/usr/lib/libSystem.B.dylib (compatibility version 1.0.0, current version 1.0.0)\n' "$target"
        if [ -n "${AUDIO_TEST_DYLIB:-}" ]; then
          printf '\t%s (compatibility version 1.0.0, current version 1.0.0)\n' "$AUDIO_TEST_DYLIB"
        fi
        touch "$AUDIO_TEST_STATE/linked-$tool"
        ;;
    esac
    ;;
  *) printf 'Unexpected fixture command: %s\n' "$name" >&2; exit 90 ;;
esac
