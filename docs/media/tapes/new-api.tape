Output new-api.gif
Source tapes/common.tape

Type "export A=localhost:3000/api/v1 && clear"
Enter
Type "vukem new shop --api 2>/dev/null | grep Created"
Show
Sleep 1s
Enter
# Hide while commands run so the GIF has no dead time; output appears on Show.
Hide
Wait@180s /[$]$/
Show
Sleep 2s
Ctrl+L
Type "cd shop && ls"
Enter
Sleep 2500ms
Ctrl+L
Type "vukem migrate && vukem db:seed"
Enter
Hide
Wait@120s /[$]$/
Show
Sleep 2500ms
Ctrl+L
Type "vukem serve > serve.log 2>&1 &"
Enter
Sleep 1500ms
Hide
Sleep 4s
Show
Type `curl -s $A/posts | grep -o '"title[^,]*' | head -3`
Enter
Sleep 3500ms
