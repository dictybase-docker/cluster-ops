#!/usr/bin/env bash
# Verify the local toolchain required by this repo's Pulumi workflow.
# Prints one line per tool with its version and exits non-zero if any is missing.
#
# Invoked by: just gcp-pulumi check-tools   (no arguments)

set -uo pipefail

source "$(dirname "$0")/../lib/check-helpers.sh"

failures=0

check_tool() {
    local bin="$1" label="$2" version=""
    if ! command -v "$bin" >/dev/null 2>&1; then
        bad "$label not found on PATH"
        return
    fi
    case "$bin" in
        pulumi)  version=$(pulumi version 2>/dev/null | head -n1) ;;
        gcloud)  version=$(gcloud version 2>/dev/null | awk '/^Google Cloud SDK/ {print $NF; exit}') ;;
        kubectl) version=$(kubectl version --client -o json 2>/dev/null | jq -r '.clientVersion.gitVersion' 2>/dev/null) ;;
        jq)      version=$(jq --version 2>/dev/null) ;;
        yq)      version=$(yq --version 2>/dev/null) ;;
        *)       version=$("$bin" --version 2>/dev/null | head -n1) ;;
    esac
    [[ -z "$version" || "$version" == "null" ]] && version="version unknown"
    ok "$label $version"
}

echo "Checking Pulumi workflow toolchain..."
echo

check_tool pulumi  "pulumi"
check_tool gcloud  "gcloud"
check_tool kubectl "kubectl"
check_tool jq      "jq"
check_tool yq      "yq"

echo
if [[ "$failures" -eq 0 ]]; then
    printf '\033[32mAll required tools present.\033[0m\n'
else
    printf '\033[31m%d tool(s) missing.\033[0m Install with: just install-tool --name <tool> --version <version>\n' "$failures"
    exit 1
fi