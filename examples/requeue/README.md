# requeue: a retry storm weak fairness does not rule out

A controller that applies a cloud resource's spec and requeues until the
cloud reports the resource ready, then marks its object ready. The cloud
provisions in two steps (pending, warming, ready) and treats an apply of
a changed spec as a replacement: provisioning starts over. Apply the
same spec every time and the controller finishes; put the reconcile's
time in the spec (a field someone added to see when the controller last
looked) and every requeue restarts the provisioning it waits for, for
ever.

That is a liveness bug no invariant states, and one that weak fairness
does not rule out: the cloud's step to ready is enabled in warming only,
and every requeue takes the resource back to pending, so the step is
never enabled throughout the cycle. The model shows it on its own
(`gotogether simulate -graph`, DESIGN §62: a lasso, the path to a fair
cycle of states where `reconciled` is false); the runs show it as the
cycle their model states go round (`hot cycle`); and the faithful
model, whose requeue changes nothing, is proved live.

| file | what it is |
|---|---|
| [model/requeue.qnt](model/requeue.qnt) | the requirement: the cloud's `create` and `advance`, the controller's `requeue` (a stutter: it changes nothing) and `finish`; `RESEND` lets an apply after the first replace the resource; `reconciled` the eventual val |
| [requeue.binding.yaml](requeue.binding.yaml) | the faithful model (`RESEND` false): `reconciled` in `simulate.holds`, the object's mark the end the model means (`simulate.terminal`) |
| [requeue.storm.binding.yaml](requeue.storm.binding.yaml) | the storm's model (`RESEND` true): `simulate -graph` fails it with the lasso |
| [cloud.go](cloud.go) | the cloud as a double: a `qenv.Service` over a lossy network, a provisioner that advances a step a second unless the resource is replaced; it records `create` and `advance` where they take effect |
| [controller.go](controller.go) | the controller: apply, then `finish` or `requeue` and sleep 1.5 s; `Run` on a qenv simulation, five seconds of faults then a settle |
| [requeue_test.go](requeue_test.go) | the models' graphs, one `qtest.System` per model; `TestMutant` says what catches each mutant |
| [testdata/mutants](testdata/mutants) | `stampSpec`: the reconcile's time in the spec, the storm; `giveUp`: the controller stops after its first requeue (a retry limit tuned to a faster cloud) |
| [cmd/requeue-sim](cmd/requeue-sim) | the simulator `run-demo.sh` runs: each seed's steps to a file for `gotogether validate` |

## What each test catches

| test | mutant | check | verdict |
|---|---|---|---|
| `TestModelLive` | none: the model alone (`qtest.Model(...).Expect(t, qtest.Live("reconciled"))`) | `requeue.binding.yaml`'s whole graph (as `gotogether simulate -graph`), every action weakly fair | `reconciled` live: no fair cycle keeps it false; the requeue, a stutter, is no step, so requeueing forever is no cycle and satisfies nothing |
| `TestStormModelHasLasso` | none: the model alone (`qtest.Refutes("reconciled")`) | `requeue.storm.binding.yaml`'s whole graph | `reconciled` hot: `init, create`, then `advance, create` forever, advance (enabled throughout) taken; `simulate -graph` fails it (exit 1) and quint replays the lasso's turn (`run-demo.sh`) |
| `TestFaithfulConforms` | none | 30 seeds, one message fault in five (lost, duplicated, late applies) | every run finishes (`Finishes`: conforms, settles, reconciled at the settle) and every action and fault kind is exercised: an apply of the same spec changes nothing |
| `TestMutant` (`stampSpec`), run by `TestMutants` | `stampSpec`: the spec carries the reconcile's time | 3 seeds without faults, against each model | the faithful model rejects the second `create` (`Some(Rejects("create"))`); against the storm's every run's steps are the model's and none settles (`None(Settles())`), and each report's hot cycle is the lasso, `advance, create` (the requeue a stutter between), gone round again and again (`All(stormCycle)`, a `Satisfies`) |
| `TestMutant` (`giveUp`), run by `TestMutants` | `giveUp`: the controller stops after one requeue | 3 seeds without faults; 30 with faults | every run settles with `reconciled` false (`All(Violates("eventually reconciled"))`); with faults some still finish (a lost response's timeout delays the second try until the resource is ready) and some do not |

A mutant is a directory of [testdata/mutants](testdata/mutants) whose
files replace the package's of the same names: `qtest.Mutants{}.Run(t)`
(`TestMutants`) builds the tests with its overlay and runs `TestMutant`
there, `qtest.Mutant()` naming it.

The two checks of liveness answer different questions. The model's
graph says whether the design can stay unreconciled forever while
every step that stays possible is taken: the faithful design cannot, the
storm's can. A run's settle says whether this run got there: `giveUp`'s
settle finds it never did, a violation; `stampSpec`'s never settles at
all, and its `hot cycle` line (evidence, no verdict) shows the cycle it
was going round when the settle's minute ran out.

## Run it

```sh
go test .                        # needs quint on PATH
./run-demo.sh                    # both models' graphs, then 20 seeds validated
MUTANT=stampSpec ./run-demo.sh   # rejected by the faithful model; the storm's shows its hot cycle (caught: exit 0)
MUTANT=giveUp ./run-demo.sh      # reconciled false at the settle (caught: exit 0)
```
