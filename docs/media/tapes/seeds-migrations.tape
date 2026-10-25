Output seeds-migrations.gif
Source tapes/common.tape

Type "vukem new site --no-install 2>/dev/null"
Enter
Sleep 1s
Type "1"
Sleep 700ms
Enter
Sleep 12s
Type "cd site && clear"
Enter
Type "vukem make:model Tag -m -f"
Show
Sleep 1s
Enter
Sleep 3s
Ctrl+L
Type "vukem make:seeder Tag"
Enter
Sleep 1s
Type "vukem make:seeder Demo --app | head -2"
Enter
Sleep 3s
Ctrl+L
Type "vukem migrate && vukem db:seed"
Enter
# Hide while commands run so the GIF has no dead time; output appears on Show.
Hide
Wait@120s /[$]$/
Show
Sleep 4s
