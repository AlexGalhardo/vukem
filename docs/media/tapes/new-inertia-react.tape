Output new-inertia-react.gif
Source tapes/common.tape

Type "vukem new ui --inertia react --no-install 2>/dev/null | grep ^C"
Show
Sleep 1s
Enter
# Hide while commands run so the GIF has no dead time; output appears on Show.
Hide
Wait@180s /[$]$/
Show
Sleep 3s
Ctrl+L
Type "cd ui && ls"
Enter
Sleep 3500ms
Ctrl+L
Type "ls resources/js resources/js/pages"
Enter
Sleep 5500ms
