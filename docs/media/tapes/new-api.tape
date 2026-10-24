Output new-api.gif
Source tapes/common.tape

Type "vukem new shop --api 2>/dev/null"
Enter
Wait@180s /[$]$/
Sleep 1s
Type "cd shop && clear && ls"
Enter
Sleep 2s
Type "vukem migrate && vukem db:seed"
Enter
Wait@120s /[$]$/
Sleep 1s
Type "clear && vukem serve > serve.log 2>&1 &"
Enter
Sleep 5s
Type "curl -s localhost:3000/api/v1/posts | head -c 420; echo"
Enter
Sleep 4s
