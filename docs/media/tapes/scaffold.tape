Output scaffold.gif
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
Type "vukem make:controller Comment --resource"
Enter
Sleep 1s
Type "vukem make:request StoreComment"
Enter
Sleep 1s
Hide
Type `sed -i '/r.Resource("posts"/a\\tr.Resource("comments", controllers.NewCommentController())' routes/web.go`
Enter
Sleep 500ms
Show
Type "grep -n Resource routes/web.go"
Enter
Sleep 2s
Type "vukem route:list | grep comments | cut -c1-82"
Enter
Sleep 4s
