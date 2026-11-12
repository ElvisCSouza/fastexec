# FastExec benchmark
This folder holds the method and the raw results behind the numbers in the [main README](../README.md#does-it-really-save-tokens-the-benchmark).
The scripts that drive the runs are not published here; everything needed to check the figures is.
## Method
- **Two arms, same everything else.** The same model gets the same prompt for the same task twice, in random order:
  `native` (Claude Code's Bash, Read, Glob, Grep, Edit and Write only) and `fastexec` (only the FastExec MCP server,
  running as an isolated daemon with its own data folder, so it never touches real statistics).
- **Model:** DeepSeek V4.1 Flash, called through Together AI, driven by the Claude Code agent loop. Prompt cache off, so
  token counts are comparable.
- **What is counted:** input + output tokens billed to the account, per run. The headline number is the geometric mean
  of the FastExec/native ratio over matched pairs (same task, same repetition), with a 95% confidence interval by
  resampling the pairs. A ratio below 1.0 means FastExec used fewer tokens.
- **Correctness first.** Every task has an automatic checker (an answer oracle, or a build and a probe for edits).
  A run only counts if the FastExec arm actually called FastExec. A second figure keeps only the pairs where both arms
  were correct, so saving tokens by answering wrongly does not show up as a gain.
- **Fixtures:** synthetic data generated for the benchmark, plus open-source repositories at pinned versions (cobra,
  hugo, Serilog, FluentValidation). No private code.
- **Round:** 68 tasks, 2 to 5 repetitions each (the round stopped at its spending cap), 398 runs, build `core-f5d1719`.
## Task kinds
| Kind | What it measures |
|---|---|
| baseline | the fixed cost per turn (system prompt + tool schemas) |
| data | find a record in one-line JSON, aggregate a 30,000-line log, read a 15 MB JSON |
| single and multi-file edits | change a constant in CRLF files byte for byte; rename a function across 8 files; thread a parameter through 4 files, then build and test |
| failures | quoting traps, PowerShell JSON, Windows-1252 text |
| large output | 20,000-line output with the error at the end; a command that only passes on the third try |
| code navigation | who calls this function, which tests reach it, where a panic was born (Go) |
| C# | the same navigation and bug-fixing tasks on Serilog and FluentValidation |
| docs | search and answer over a generated corpus of Markdown files |
| terminal | files with spaces and accents, a synthetic git history, mixed logs, child processes, `.cmd` and `.ps1` scripts, an 80 MB sparse file |
| software-engineering subset | fix a real bug from the history of cobra or hugo; the commit's own test is the checker (lighter than SWE-bench, not the official one) |
| multi-step | batch-read 12 config files, a ~1 MB output, a ~50 s job with progress, a background server with cleanup, a search over 3,000 files, a 3-stage CSV-to-JSON pipeline |
## Files
- `results.jsonl`: one line per run (task, arm, repetition, tokens, turns, bytes returned, wall time, CPU, memory, cost,
  whether it was correct and valid).
- `report.pt.md`: the full report generated from those lines (in Portuguese): per-task table, per-kind table, fixed
  cost per turn, latency, CPU, memory, cost, correctness and use of the code graph.
## Limits
One model, synthetic and open-source fixtures, few repetitions, cache off. Treat the numbers as a measurement of this
setup, not a promise for yours.