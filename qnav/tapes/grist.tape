# A grist rejection: counters' blind mutant's run, opened at its failing
# step, says why in grist's words (a concurrent append); the filter keeps
# the branches it matches.
Output media/grist.gif

Require gotogether-nav
Set Shell bash
Set FontSize 14
Set Width 1000
Set Height 680
Set Padding 12
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set Framerate 6

# the terminal's size pinned in rows and columns: the browser's font
# metrics (its fonts, its version) decide how many rows a height in
# pixels holds, and a screen that fills them differed between machines
Hide
Type "stty rows 35 cols 105 && cd ../../examples/counters && clear" Enter
Show

Type "gotogether-nav -binding counters.binding.yaml -run ../../qnav/tapes/testdata/blind.jsonl" Sleep 400ms Enter
Wait+Screen@60s /branches \(86\)/
Sleep 1.5s
Type "/showCounter" Sleep 300ms Enter
Wait+Screen /grist: event 1/
Sleep 4s
Screenshot media/grist.png
Sleep 500ms
Type "q"
Sleep 300ms
