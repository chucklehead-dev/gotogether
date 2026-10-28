# A failing run: hello's NoDedupe at its failing choice. Read the steps,
# the model's state and the verdict, take the run's option (the duplicate's
# second apply, rejected, and why), go back, take another, save it.
Output media/hello.gif

Require gotogether-nav
Set Shell bash
Set FontSize 14
Set Width 1000
Set Height 720
Set Padding 12
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set Framerate 12
Env GOTOGETHER_CORPUS "/tmp/gotogether-nav-tape-corpus"

Hide
Type "cd ../../examples/hello && rm -rf /tmp/gotogether-nav-tape-corpus && clear" Enter
Show

Type "gotogether-nav -run 'TestNoDedupeCountsRetries#1:seed=1' -at fail" Sleep 400ms Enter
Wait+Screen@120s /next choice: sched.task/
Sleep 2.5s
Enter
Wait+Screen /steps so far \(2\)/
Sleep 3s
Screenshot media/hello.png
Sleep 500ms
Backspace
Wait+Screen /path: fail\n/
Sleep 1s
Up Sleep 400ms Enter
Wait+Screen /fault.rpc.counter.response/
Sleep 2s
Type "s"
Wait+Screen /saved to the corpus/
Sleep 2s
Type "q"
Sleep 300ms
Hide
Type "rm -rf /tmp/gotogether-nav-tape-corpus" Enter
