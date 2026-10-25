Output tests.gif
Source tapes/common.tape

# Hidden helpers: P lists short-named core packages, `short` trims the real
# `go test` output to "ok <pkg> <time>" and appends a pass/fail summary.
Type `P="./router ./web ./z ./orm ./auth ./cache ./csrf ./events ./ratelimit ./view"`
Enter
Type `short() { sed -u 's#github.com/AlexGalhardo/vukem/##' | awk '/^ok/{n++; print $1,$2,$3; fflush()} /^FAIL|^---/{f=1} END{if(f) print "FAILED"; else print "all " n " packages passed"}'; }`
Enter
Type "cd /src && clear"
Enter
Type 'go test -count=1 $P | short'
Show
Sleep 1s
Enter
Wait@300s /[$]$/
Sleep 9s
