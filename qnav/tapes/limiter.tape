# A model on the limiter: take admit("a"), then admit("b"), read why
# admit is disabled at the limit, go back.
Output media/limiter.gif

Require gotogether-nav
Set Shell bash
Set FontSize 14
Set Width 1000
Set Height 620
Set Padding 12
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set Framerate 12

Hide
Type "cd ../../validate/testdata && clear" Enter
Show

Type "gotogether-nav -binding limiter.yaml" Sleep 400ms Enter
Wait+Screen@30s /branches \(3\)/
Sleep 1.5s
Enter
Wait+Screen /admitted = Set\("a"\)/
Sleep 1s
Down Sleep 500ms Enter
Wait+Screen /admitted.size\(\) < LIMIT is false/
Sleep 3s
Screenshot media/limiter.png
Sleep 500ms
Backspace
Wait+Screen /path: init › admit\("a"\)\n/
Sleep 1.5s
Type "q"
Sleep 300ms
