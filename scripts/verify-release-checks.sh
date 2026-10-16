#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# These existing integration tests invoke native GNU grep or copy a native
# shell under a fixture name. The Forgejo host runner provides BusyBox tools;
# execute this group on the documented reference host before tagging instead.
reference_tests='^(TestGrepPagesBeforeSavingNativeResults|TestFilteredInspectionRetainsExplicitSelectedPage|TestStandardInspectionAndImplementationRolesDelegateAndSaveResults|TestProcessHelperBinaryOutputAndPATH|TestGrepBackendSelectionUsesConfiguredPATH|TestGrepPrefersRGAndWorksWithOnlyGNUGrepOnPATH|TestReadOnlyToolsDirectAndGeneratedOnFilesAndGit)$'

case "${1:-}" in
  portable)
    go test ./... -skip "$reference_tests"
    ;;
  reference)
    go test ./internal/app ./internal/jsruntime -run "$reference_tests" -count=1
    ;;
  *)
    echo "usage: $0 portable|reference" >&2
    exit 2
    ;;
esac
