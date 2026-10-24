Set Shell "bash"
Set Theme "Catppuccin Mocha"
Set FontSize 13
Set Width 1100
Set Height 680
Set Padding 16
Set TypingSpeed 40ms
Set Framerate 15
Set WindowBar Colorful
Set BorderRadius 8

# Setup runs hidden so the GIF starts on the typed command.
# warm.sh primes the Go module and build caches so `vukem new` takes seconds.
Hide
Type "export PS1='$ ' && rm -rf /tmp/demo && mkdir -p /tmp/demo && bash /vhs/tapes/warm.sh; echo WARM-$((1+1))"
Enter
Wait+Screen@600s /WARM-2/
Type "cd /tmp/demo && clear"
Enter
Sleep 300ms
Show
