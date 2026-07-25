Output gif/timeouts.gif
Output webm/timeouts.webm

Set Shell "zsh"
Set FontSize 18
Set Width 1200
Set Height 800
Set TypingSpeed 60ms
Set Padding 24

Hide
Type `DEMO_ROOT=$(mktemp -d)`
Enter
Type `jobman() { ./bin/jobman --state-dir "$DEMO_ROOT/state" "$@"; }`
Enter
Type `jobman clean --all --force`
Enter
Wait
Type "clear"
Enter
Show


Type `jobman run --name slow --run-timeout 5s -- sleep 20`
Sleep 250ms
Enter
Sleep 250ms
Enter
Sleep 1.75s

Type `jobman wait slow`
Sleep 500ms
Enter
Sleep 1.5s
Enter
Sleep 2s

Type `jobman show slow`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s
