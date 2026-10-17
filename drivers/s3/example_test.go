package s3_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/drivers/s3"
	"github.com/AlexGalhardo/vukem/storage"
)

// The disk satisfies storage.Disk, so the storage helpers work on it.
func Example() {
	ctx := context.Background()

	src, err := config.LoadSource(os.DirFS("."), os.LookupEnv, nil)
	if err != nil {
		log.Fatal(err)
	}

	cfg, err := config.Load[s3.Config](src) // S3_BUCKET, S3_REGION, ...
	if err != nil {
		log.Fatal(err)
	}

	disk, err := s3.New(cfg)
	if err != nil {
		log.Fatal(err)
	}

	if err := storage.PutBytes(ctx, disk, "reports/2026-10.csv", []byte("id,total\n")); err != nil {
		log.Fatal(err)
	}
}

// Presigned URLs let a browser download (or upload) one object directly,
// without credentials. Signing is local: no request is sent.
func ExampleDisk_PresignGet() {
	disk, err := s3.New(s3.Config{
		Bucket:          "invoices",
		Region:          "us-east-1",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: config.NewSecret("example-secret"),
	})
	if err != nil {
		log.Fatal(err)
	}

	link, err := disk.PresignGet("2026/42.pdf", 15*time.Minute)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(strings.HasPrefix(link, "https://invoices.s3.us-east-1.amazonaws.com/2026/42.pdf?"))
	fmt.Println(strings.Contains(link, "X-Amz-Expires=900"))

	// Keys are storage paths: traversal is refused before anything is signed.
	_, err = disk.PresignGet("../secrets", time.Minute)
	fmt.Println(errors.Is(err, storage.ErrInvalidPath))
	// Output:
	// true
	// true
	// true
}
