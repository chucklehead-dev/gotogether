// Command bankdriver is an external driver for gotogether shrink's test:
// it plays a list of actions on a bank with an overdraft bug (a
// withdrawal is refused only from an empty account, so one larger than a
// positive balance goes through) and writes the steps it recorded as a
// step log for qtest/testdata/bank.yaml. A cancel undoes the account's
// last deposit; with no deposit to undo it cannot be done, and the
// driver exits 3 (gotogether shrink -diverged-exit 3). With -reset FILE
// it only appends a line to FILE: a reset that counts itself. The other
// flags misbehave as drivers do: -background leaves a child running that
// holds its output open, -hang sleeps before playing, -cd plays from
// another directory, -tear-single ends the log of a run of one action in
// a line cut off.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/chucklehead-dev/gotogether/qtrace"
)

type action struct {
	Op      string `json:"op"`
	Account string `json:"account"`
	Amount  int64  `json:"amount"`
}

func main() {
	actionsPath := flag.String("actions", "", "the actions to play, a JSON array")
	stepsPath := flag.String("steps", "", "where to write the steps")
	reset := flag.String("reset", "", "append a line to this file and exit")
	background := flag.Bool("background", false, "leave a child running that holds stdout open")
	hang := flag.Duration("hang", 0, "sleep this long first")
	cd := flag.String("cd", "", "change to this directory first")
	tear := flag.Bool("tear-single", false, "cut off the last line of a run of one action's log")
	flag.Parse()
	if *background {
		child := exec.Command("sleep", "30")
		child.Stdout = os.Stdout
		exit(child.Start())
	}
	time.Sleep(*hang)
	if *cd != "" {
		exit(os.Chdir(*cd))
	}
	if *reset != "" {
		f, err := os.OpenFile(*reset, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, err = f.WriteString("reset\n")
			err = errors.Join(err, f.Close())
		}
		exit(err)
		return
	}
	data, err := os.ReadFile(*actionsPath)
	exit(err)
	var actions []action
	exit(json.Unmarshal(data, &actions))
	balances := map[string]int64{}
	deposits := map[string][]int64{}
	var steps []qtrace.Step
	record := func(name, account string, amount int64) {
		steps = append(steps, qtrace.Step{Recorder: "bank", Seq: uint64(len(steps) + 1), Action: name,
			Args: map[string]any{"account": account, "amount": amount}, Obs: map[string]any{"balance": balances[account]}})
	}
	for _, a := range actions {
		switch a.Op {
		case "deposit":
			balances[a.Account] += a.Amount
			deposits[a.Account] = append(deposits[a.Account], a.Amount)
			record("deposit", a.Account, a.Amount)
		case "withdraw":
			if balances[a.Account] == 0 { // the bug: only an empty account refuses
				continue
			}
			balances[a.Account] -= a.Amount
			record("withdraw", a.Account, a.Amount)
		case "cancel":
			ds := deposits[a.Account]
			if len(ds) == 0 {
				fmt.Fprintln(os.Stderr, "cancel: no deposit to undo")
				os.Exit(3)
			}
			last := ds[len(ds)-1]
			deposits[a.Account] = ds[:len(ds)-1]
			balances[a.Account] -= last
			record("withdraw", a.Account, last)
		default:
			exit(fmt.Errorf("unknown op %q", a.Op))
		}
	}
	f, err := os.Create(*stepsPath)
	exit(err)
	exit(errors.Join(qtrace.WriteJSONL(f, steps), f.Close()))
	if *tear && len(actions) == 1 && len(steps) > 0 { // a next step's line, cut off
		f, err := os.OpenFile(*stepsPath, os.O_APPEND|os.O_WRONLY, 0o644)
		exit(err)
		_, err = f.WriteString(`{"quint.action":"depo`)
		exit(errors.Join(err, f.Close()))
	}
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "bankdriver:", err)
		os.Exit(1)
	}
}
