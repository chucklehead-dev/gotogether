# A recorded jobqueue incident: restart loses the lease, but the planted
# implementation bug accepts its stale ack. The TUI opens before call 5
# (the inferred restart is a model step; the ack is captured record seq 4).
Output media/incident.gif

Require gotogether-nav
Require go
Set Shell bash
Set FontSize 14
Set Width 1000
Set Height 680
Set Padding 12
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set Framerate 6

Hide
Type 'stty rows 35 cols 105 && cd ../../examples/jobqueue && incident_dir=$(mktemp -d) && go run ./cmd/jobqueue-incident -out "$incident_dir" && cp jobqueue.binding.yaml "$incident_dir/" && cp -R model "$incident_dir/" && cd "$incident_dir" && clear' Enter
Show

Type "gotogether-nav -binding jobqueue.binding.yaml -run incident.jsonl" Sleep 400ms Enter
Wait+Screen@60s /before call 5/
Sleep 1.5s
Type "e"
Wait+Screen /captured history evidence/
Sleep 700ms
Enter
Wait+Screen /recorder: jobqueue-incident-incident · seq 4/
Sleep 2s
Screenshot media/incident.png
Backspace
Sleep 300ms
Backspace
Wait+Screen /before call 5/
Type "/ack" Sleep 300ms Enter
Wait+Screen /o == Stale is false/
Sleep 2s
# This is a model move, not a change to the recorded incident or its verdict.
Enter
Sleep 1.5s
Type "e"
Wait+Screen /independent of model cursor/
Enter
Wait+Screen /recorder: jobqueue-incident-incident · seq 4/
Sleep 1s
Backspace
Sleep 300ms
Backspace
Sleep 300ms
Backspace
Wait+Screen /before call 5/
Sleep 1s
Type "q"
Sleep 300ms
Hide
Type 'cd / && rm -rf "$incident_dir"' Enter
