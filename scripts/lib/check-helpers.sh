#!/usr/bin/env bash
# Shared PASS/FAIL/INFO/WARN helpers for `just` check recipes.
#
# Sourced (never executed) from recipes via:
#   source "{{ justfile_directory() }}/scripts/lib/check-helpers.sh"
#
# Contract:
#   - The sourcing recipe MUST initialize `failures=0` before its first bad().
#   - `bad` increments the recipe-scope `failures` variable; summary/exit
#     lines stay in the recipe (they point at recipe-specific docs).
#   - CHECK_COLOR=1 (default) prints ANSI colors; CHECK_COLOR=0 prints plain
#     PASS/FAIL/INFO/WARN for recipes whose output is piped. Read at each
#     call, so it can be set before or after sourcing.
#   - ok/bad accept one arg (message) or two (label + detail). Two-arg form
#     pads the label to CHECK_LABEL_WIDTH (default 10; cluster recipes set 24).

_check_pfx() {
    # $1 = word (PASS/FAIL/INFO/WARN), $2 = ansi color
    if [ "${CHECK_COLOR:-1}" = "0" ]; then
        printf '%s  ' "$1"
    else
        printf '\033[%sm%s\033[0m  ' "$2" "$1"
    fi
}

ok() {
    if [ "$#" -eq 2 ]; then
        printf '%s%-*s %s\n' "$(_check_pfx PASS 32)" "${CHECK_LABEL_WIDTH:-10}" "$1" "$2"
    else
        printf '%s%s\n' "$(_check_pfx PASS 32)" "$1"
    fi
}

bad() {
    if [ "$#" -eq 2 ]; then
        printf '%s%-*s %s\n' "$(_check_pfx FAIL 31)" "${CHECK_LABEL_WIDTH:-10}" "$1" "$2"
    else
        printf '%s%s\n' "$(_check_pfx FAIL 31)" "$1"
    fi
    failures=$((failures + 1))
}

info() {
    printf '%s%s\n' "$(_check_pfx INFO 34)" "$1"
}

warn() {
    printf '%s%s\n' "$(_check_pfx WARN 33)" "$1"
}