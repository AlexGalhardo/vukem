package s3

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
)

// createBucketConfiguration is the CreateBucket request body.
type createBucketConfiguration struct {
	XMLName            xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ CreateBucketConfiguration"`
	LocationConstraint string   `xml:"LocationConstraint"`
}

// CreateBucket creates the disk's bucket in its region (S3_REGION, sent as
// the LocationConstraint except for us-east-1 and auto), for dev setup and
// tests against a fresh local S3 (RustFS, MinIO). A bucket the credentials
// already own is not an error; a name taken by another account is an
// *Error with Code "BucketAlreadyExists".
func (d *Disk) CreateBucket(ctx context.Context) error {
	var body []byte

	// us-east-1 is the default location and S3 rejects it as an explicit
	// constraint; R2's "auto" region is not a location either.
	if r := d.signer.region; r != "us-east-1" && r != "auto" {
		b, err := xml.Marshal(createBucketConfiguration{LocationConstraint: d.signer.region})
		if err != nil {
			return fmt.Errorf("s3: create bucket: %w", err)
		}

		body = b
	}

	h := http.Header{}
	if body != nil {
		h.Set("Content-Type", "application/xml")
	}

	resp, err := d.do(ctx, http.MethodPut, "", nil, h, body)
	if err != nil {
		return fmt.Errorf("s3: create bucket %s: %w", d.bucket, err)
	}

	if resp.StatusCode/100 == 2 {
		return resp.Body.Close()
	}

	err = readError("create bucket", d.bucket, resp)
	if e, ok := errors.AsType[*Error](err); ok && e.Code == "BucketAlreadyOwnedByYou" {
		return nil
	}

	return err
}
