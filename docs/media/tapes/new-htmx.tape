Output new-htmx.gif
Source tapes/common.tape

Type "vukem new site --no-install 2>/dev/null"
Show
Sleep 1s
Enter
Sleep 3s
Type "1"
Sleep 800ms
Enter
Sleep 1s
# Hide while the scaffold is generated (a few seconds with warm caches).
Hide
Sleep 12s
Ctrl+L
Type "cd site && ls"
Show
Sleep 500ms
Enter
Sleep 3s
Ctrl+L
Type "vukem route:list 2>/dev/null | head -9 | cut -c1-42"
Enter
Sleep 5s
