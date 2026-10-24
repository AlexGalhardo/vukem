Output seeds-migrations.gif
Source tapes/common.tape

Type "vukem new site --no-install 2>/dev/null"
Enter
Sleep 1s
Type "1"
Sleep 700ms
Enter
Wait@180s /[$]$/
Type "cd site && clear"
Enter
Type "vukem make:model Tag -m -f"
Enter
Sleep 1s
Type "vukem make:seeder Tag"
Enter
Sleep 1s
Type "vukem make:seeder Demo --app"
Enter
Sleep 2s
Type "vukem migrate"
Enter
Sleep 3s
Type "vukem db:seed"
Enter
Sleep 4s
