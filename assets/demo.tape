# Records the chat TUI for the README: `vhs assets/demo.tape` writes
# assets/demo.gif (https://github.com/charmbracelet/vhs). Run it from the repo
# root with a configured provider (`ouija setup`); it asks one real question.
Output assets/demo.gif

Set Shell "bash"
Set FontSize 15
Set Width 1200
Set Height 760
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 45ms

Hide
Type "ouija chat" Enter
Sleep 300ms
Show

# The intro: the planchette spells the name.
Sleep 2.5s

Type "/"
Sleep 1.2s
Escape
Sleep 400ms

Type "what does the budget tracker in engine/budget.go do?"
Sleep 400ms
Enter
# Tools fill in live, then the reply streams.
Sleep 20s

Ctrl+F
Type "budget"
Sleep 1.5s
Enter
Sleep 1s
Escape
Sleep 1s

Ctrl+C
Sleep 2s
