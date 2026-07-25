Output gif/input.gif
Output webm/input.webm

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

Type `jobman run --name parrot --stdin live -- cat`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `echo hello | jobman input parrot`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman input --eof parrot`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman show parrot`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s

Type `jobman logs -f parrot`
Sleep 500ms
Enter
Sleep 500ms
Enter
Sleep 3s