Output new-htmx.gif
Source tapes/common.tape

Type "vukem new site --no-install 2>/dev/null"
Enter
Sleep 1s
Type "1"
Sleep 700ms
Enter
Wait@180s /[$]$/
Sleep 2s
Type "cd site && clear && ls"
Enter
Sleep 2s
Type "vukem route:list | cut -c1-82"
Enter
Sleep 4s
