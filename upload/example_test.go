package upload_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/upload"
)

func ExampleSave() {
	ctx := context.Background()
	disk := storage.NewMemory("/storage")

	rules := upload.Images(2 << 20)
	rules.Dir = "avatars"

	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	f, err := upload.Save(ctx, disk, rules, "avatar", "../../Me.png", bytes.NewReader(png))
	if err != nil {
		fmt.Println(err)
		return
	}

	// The stored name is random; the client's name is sanitized metadata.
	fmt.Println(f.Type, f.Name, strings.HasPrefix(f.Path, "avatars/"))

	// Content that does not match an allowed type is rejected.
	_, err = upload.Save(ctx, disk, rules, "avatar", "evil.png", strings.NewReader("<script>"))
	fmt.Println(err != nil)
	// Output:
	// image/png Me.png true
	// true
}

func ExampleSanitizeName() {
	fmt.Println(upload.SanitizeName("../../etc/pass\x00wd.txt"))
	// Output: passwd.txt
}
