package tui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
)

// Failing is the navigator at a recorded run's failing step: the state
// after the most calls the model accepts, and why it rejects the next
// (the navigator's own explanation, without its first line's advice).
func Failing(ch *validate.Checker, start validate.NavStart) (ModelNav, string, string, error) {
	start.At = -1
	_, rejected := ch.Nav(start)
	if rejected == nil {
		return nil, "", "", errors.New("the model accepts every call of the run: no failing step")
	}
	var last *validate.Navigator
	for k := 0; ; k++ {
		start.At = k
		n, err := ch.Nav(start)
		if err != nil {
			if last == nil {
				return nil, "", "", err
			}
			return last, fmt.Sprintf("the state before call %d, which the model rejects", k), tidyWhy(rejected.Error()), nil
		}
		last = n
	}
}

// tidyWhy is the navigator's error for a call the model rejects, as the
// navigator shows it: the call and why, without the advice the command
// line gives (which call to open the run at: the navigator is there).
func tidyWhy(s string) string {
	s = strings.TrimPrefix(s, "nav: ")
	if i := strings.Index(s, " is not a transition"); i >= 0 {
		if j := strings.Index(s, "\n"); j > i {
			s = s[:i] + s[j:]
		}
	}
	return s
}
