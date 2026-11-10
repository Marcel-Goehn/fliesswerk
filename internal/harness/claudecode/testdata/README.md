# Claude Code `stream-json` transcripts

Golden files for the contract test in `parse_test.go`. They are fed line by
line into `Parse`, and each must resolve to exactly one Step outcome.

These were written by hand to match Claude Code 2.1.319's `stream-json` format.
Before each release the maintainer re-records them with a real `claude` and
updates the expected events in `parse_test.go` if the format has moved.
Re-record in an empty directory, with `--safe-mode` so no personal settings,
plugins or paths end up in the files:

```sh
SCHEMA='…' # the value of harness.ReportSchema (internal/harness/report.go)
cd "$(mktemp -d)" && echo hello > a.txt

# done.jsonl: a Step that reads, runs a command and reports
echo "Read a.txt, then report outcome done." | claude -p --output-format stream-json --verbose \
  --permission-mode bypassPermissions --permission-prompts none \
  --settings '{"sandbox":{"enabled":false}}' --json-schema "$SCHEMA" \
  --no-session-persistence --safe-mode --model claude-haiku-5-5 > done.jsonl

# auth.jsonl: a bad API key, which costs nothing
echo "say hi" | ANTHROPIC_API_KEY=sk-ant-bogus HOME="$(mktemp -d)" claude -p \
  --output-format stream-json --verbose --no-session-persistence --safe-mode > auth.jsonl
```

Replace the home directory and any session-specific paths in the recorded files
with `/work` before committing them.

Rate-limit, overload and execution-error transcripts can't be provoked on
demand. Take them from a real Run's `.jsonl` log when one occurs.
