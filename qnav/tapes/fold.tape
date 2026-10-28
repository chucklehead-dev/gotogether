# Folding a linear stretch: the ticks model's five single ticks in one
# move (f), a node of two branches folds nothing, back to init.
Output media/fold.gif

Require gotogether-nav
Set Shell bash
Set FontSize 14
Set Width 1000
Set Height 520
Set Padding 12
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set Framerate 12

# the terminal's size pinned in rows and columns: the browser's font
# metrics (its fonts, its version) decide how many rows a height in
# pixels holds, and a screen that fills them differed between machines
Hide
Type "stty rows 27 cols 105 && cd ../../validate/testdata && clear" Enter
Show

Type "gotogether-nav -binding ticks.yaml" Sleep 400ms Enter
Wait+Screen@30s /branches \(1\)/
Sleep 1.5s
Type "f"
Wait+Screen /c = 5/
Sleep 2s
Screenshot media/fold.png
Sleep 500ms
Type "f"
Wait+Screen /nothing to fold/
Sleep 2s
Backspace
Wait+Screen /c = 0/
Sleep 1.5s
Type "q"
Sleep 300ms
