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
    # Workflow-command messages must carry newlines as %0A; a raw newline
    # ends the message.
    local tail
    tail="$(tail -n 15 "$log" | sed -e 's/%/%25/g' -e 's/^/%0A/' | tr -d '\n')"
    printf '::error::%s - last log lines:%s\n' "$what" "$tail"
  fi
  return 1
}
