# Eval

Examines Yuanfang on incidents staged in the local lab and grades each
diagnosis against the ground truth. Tracking issue: #15.

```sh
make lab-up
make eval EVAL_ARGS="-scenario lock-wait -runs 3"
```

Every run calls the model for real, so it costs tokens. It also restarts the
lab MySQL container. The eval refuses to run unless the container
`yuanfang-lab-mysql` publishes MySQL only on `127.0.0.1:13306` and the server
there reports the container's hostname.

## What a run does

1. Restart the lab MySQL, so no state leaks from an earlier run.
2. Cause the incident with the `app` account, recording the real connection
   IDs as the ground truth.
3. Poll the server with root until the incident is observable and has been
   stable for 3 seconds (at most 30). Otherwise the run is `ERROR`.
4. Run the built `yuanfang ask` as a separate process with a per-run config
   (production defaults, `allow_explain: false`) and only the complaint.
   Yuanfang has no tool that reads local files, and the ground truth is never
   passed to it or written to MySQL.
5. Recheck that the incident still held when Yuanfang finished; if not, `ERROR`.
6. Grade the case Yuanfang wrote, then clean up (always, also after a failure).

Outcomes: `PASS`, `FAIL` (a core check failed), or `ERROR` (the lab or the
incident misbehaved; such runs say nothing about Yuanfang and are left out of
the pass rate).

## Scenarios

| Name | Incident | Question |
|---|---|---|
| `lock-wait` | Session A updates `shop.orders` and stays idle in its transaction; session B updates the same row and waits. | orders 表的更新一直卡住了，帮我看看数据库有什么问题。 |

Checks are rules, not a model judge. Core checks (a failure fails the run):
the cited evidence exists, no destructive command, the root cause names the
blocking connection and a lock on orders, the cited evidence shows the
blocking (judged by column names, whichever probe produced it), and no KILL
of any connection but the blocker. Recorded but not failing: the transaction
is described as idle or uncommitted, confidence is high, a recommendation
ends the blocker.

## Results

`eval/results/<time>/` (gitignored):

```
scorecard.md                one row per run, a column per check
results.json
yuanfang                    the binary under test
lock-wait-1/
  ground-truth.json         expected + observed
  grading.json              outcome and every check with its reason
  yuanfang.yaml             the config Yuanfang ran with
  yuanfang.log
  cases/<case-id>/          the case as Yuanfang wrote it
```
