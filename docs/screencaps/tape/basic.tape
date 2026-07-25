Output gif/basic.gif
Output webm/basic.webm

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

Type `jobman --version`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman run --name demo -- sh -c 'echo sleeping...; sleep 10; echo done'`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman list`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman logs -f demo`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman show demo`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s
