Output gif/retries.gif
Output webm/retries.webm

Set Shell "zsh"
Set FontSize 18
Set Width 1200
Set Height 800
Set TypingSpeed 60ms
Set Padding 24

Hide
Type `DEMO_ROOT=$(mktemp -d)`
Enter
Type `jobman() { ../../bin/jobman --state-dir "$DEMO_ROOT/state" "$@"; }`
Enter
Type `jobman clean --all --force`
Enter
Wait
Type "clear"
Enter
Show

Type `jobman run --name retry --retries 2 -- sh -c 'sleep 3; echo failed; exit 1'`
Sleep 300ms
Enter
Sleep 300ms
Enter
Sleep 3s

Type `jobman wait retry`
Sleep 500ms
Enter
Sleep 5.5s
Enter

Type `jobman show retry`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Hide
Type `jobman clean --all --force`
Enter
Wait
Type `rm -rf "$DEMO_ROOT"`
Enter
Wait
