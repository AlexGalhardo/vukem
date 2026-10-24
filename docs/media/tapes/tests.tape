Output tests.gif
Source tapes/common.tape

Type "cd /src && clear"
Enter
Type "go test -count=1 ./router/... ./web/... ./orm/... ./z/... ./auth/... ./cache/... ./csrf/... ./events/... ./queue/... ./ratelimit/... ./view/..."
Enter
Wait@300s /[$]$/
Sleep 6s
