package storage_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/AlexGalhardo/vukem/storage"
)

func Example() {
	ctx := context.Background()

	// Memory is for tests; apps use storage.NewLocal or drivers/s3.
	disk := storage.NewMemory("/storage")

	_ = storage.PutBytes(ctx, disk, "avatars/1.txt", []byte("hello"))

	data, _ := storage.Get(ctx, disk, "avatars/1.txt")
	paths, _ := disk.List(ctx, "avatars")
	fmt.Println(string(data), paths, disk.URL("avatars/1.txt"))
	// Output: hello [avatars/1.txt] /storage/avatars/1.txt
}

func ExampleClean() {
	p, err := storage.Clean("a/./b.txt")
	fmt.Println(p, err)

	_, err = storage.Clean("../etc/passwd")
	fmt.Println(errors.Is(err, storage.ErrInvalidPath))
	// Output:
	// a/b.txt <nil>
	// true
}
