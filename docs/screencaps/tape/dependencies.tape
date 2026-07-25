Output gif/dependencies.gif
Output webm/dependencies.webm

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

Type `jobman run --name train -- sh -c 'echo training...; sleep 15; echo training done'`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman run --name test --after-success train -- echo testing done`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman pause train`
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

Type `jobman resume train`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman logs -f train`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 1.5s

Type `jobman logs -f test`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 1.5s

Type `jobman list`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s