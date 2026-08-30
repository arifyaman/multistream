# Sourced by the ffbuild workflow steps. report_build_failure publishes a
# build log to the job's step summary (public on the run page, no cap) and as
# ONE multi-line annotation. GitHub keeps at most ~10 annotations per job, so
# a single annotation carrying the tail beats one per log line.
report_build_failure() {
  # $1 = what failed, $2 = log file
  local what="$1" log="$2"
  if [ -f "$log" ]; then
    if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
      {
        echo ""
        echo "### $what"
        echo '```'
        tail -n 60 "$log"
        echo '```'
      } >> "$GITHUB_STEP_SUMMARY"
    fi
    printf '::error::%s - last log lines:\n%s\n' "$what" "$(tail -n 15 "$log")"
  fi
  return 1
}
