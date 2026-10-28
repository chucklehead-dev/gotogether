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

Hide
Type "cd ../../validate/testdata && clear" Enter
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
