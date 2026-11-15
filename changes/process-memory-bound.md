- `simulate -graph -process-memory MiB` bounds the whole process's memory as
  the Go runtime maps it (`model.GraphSearch.ProcessBytes`,
  `SimulateOptions.ProcessBytes`, `qeval.Bound.ProcessBytes`): read while the
  search runs and confirmed after a collection, a reading past the bound cuts
  the graph as a memory bound (`processCut`, `processSeen`), where `-memory`'s
  retained-storage estimate let a search reach 1.18 times its budget in
  resident memory and kill a CI runner (golem's, #144). Every graph's JSON
  report carries `peakResident`, the process's resident high-water mark.
