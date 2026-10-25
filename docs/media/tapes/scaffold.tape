Output scaffold.gif
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
Type "vukem make:controller Comment --resource"
Show
Sleep 1s
Enter
Sleep 1s
Type "vukem make:request StoreComment"
Enter
Sleep 3s
Ctrl+L
Hide
Type `sed -i '/r.Resource("posts"/a\\tr.Resource("comments", controllers.NewCommentController())' routes/web.go`
Enter
Sleep 500ms
Ctrl+L
Show
Type "grep -n comments routes/web.go | expand -t1"
Enter
Sleep 3s
Ctrl+L
Type "vukem route:list | grep comments | cut -c1-52"
Enter
Sleep 4s
