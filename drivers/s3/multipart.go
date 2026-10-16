package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const abortTimeout = 30 * time.Second

type completePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type completeUpload struct {
	XMLName xml.Name       `xml:"CompleteMultipartUpload"`
	Parts   []completePart `xml:"Part"`
}

// multipart uploads first (one full part) and the rest of r in parts of
// partSize, aborting the upload on any failure so no orphaned parts are
// billed.
func (d *Disk) multipart(ctx context.Context, key, ctype string, first *bytes.Buffer, r io.Reader) (err error) {
	id, err := d.createUpload(ctx, key, ctype)
	if err != nil {
		return err
	}

	defer func() {
		if err != nil {
			// The caller's context may be what failed; the abort still runs.
			actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), abortTimeout)
			defer cancel()

			if aerr := d.abortUpload(actx, key, id); aerr != nil {
				err = errors.Join(err, aerr)
			}
		}
	}()

	var parts []completePart

	buf := first

	for n := 1; ; n++ {
		if n > maxParts {
			return fmt.Errorf("s3: put %s: body exceeds %d parts of %d bytes", key, maxParts, d.partSize)
		}

		etag, err := d.uploadPart(ctx, key, id, n, buf.Bytes())
		if err != nil {
			return err
		}

		parts = append(parts, completePart{PartNumber: n, ETag: etag})

		buf.Reset()

		read, err := io.CopyN(buf, r, d.partSize)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("s3: put %s: read: %w", key, err)
		}

		if read == 0 {
			break
		}
	}

	return d.completeUpload(ctx, key, id, parts)
}

func (d *Disk) createUpload(ctx context.Context, key, ctype string) (string, error) {
	resp, err := d.do(ctx, http.MethodPost, key, url.Values{"uploads": {""}}, http.Header{"Content-Type": {ctype}}, nil)
	if err != nil {
		return "", fmt.Errorf("s3: create upload %s: %w", key, err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", readError("create upload", key, resp)
	}
	defer resp.Body.Close()

	var res struct {
		UploadID string `xml:"UploadId"`
	}

	if err := xml.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&res); err != nil || res.UploadID == "" {
		return "", fmt.Errorf("s3: create upload %s: no upload id in response", key)
	}

	return res.UploadID, nil
}

func (d *Disk) uploadPart(ctx context.Context, key, id string, n int, body []byte) (string, error) {
	q := url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {id}}

	resp, err := d.do(ctx, http.MethodPut, key, q, nil, body)
	if err != nil {
		return "", fmt.Errorf("s3: upload part %d of %s: %w", n, key, err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", readError("upload part "+strconv.Itoa(n), key, resp)
	}

	drain(resp)

	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("s3: upload part %d of %s: no ETag in response", n, key)
	}

	return etag, nil
}

func (d *Disk) completeUpload(ctx context.Context, key, id string, parts []completePart) error {
	body, err := xml.Marshal(completeUpload{Parts: parts})
	if err != nil {
		return err
	}

	h := http.Header{"Content-Type": {"application/xml"}}

	resp, err := d.do(ctx, http.MethodPost, key, url.Values{"uploadId": {id}}, h, body)
	if err != nil {
		return fmt.Errorf("s3: complete upload %s: %w", key, err)
	}

	if resp.StatusCode != http.StatusOK {
		return readError("complete upload", key, resp)
	}
	defer resp.Body.Close()

	// S3 can answer 200 and still report an error in the body.
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return fmt.Errorf("s3: complete upload %s: %w", key, err)
	}

	// Unmarshal only succeeds when the root element is <Error>.
	var eb struct {
		XMLName   xml.Name `xml:"Error"`
		Code      string   `xml:"Code"`
		Message   string   `xml:"Message"`
		RequestID string   `xml:"RequestId"`
	}

	if xml.Unmarshal(b, &eb) == nil {
		return &Error{
			Op: "complete upload", Key: key, StatusCode: resp.StatusCode,
			Code: eb.Code, Message: eb.Message, RequestID: eb.RequestID,
		}
	}

	return nil
}

func (d *Disk) abortUpload(ctx context.Context, key, id string) error {
	resp, err := d.do(ctx, http.MethodDelete, key, url.Values{"uploadId": {id}}, nil, nil)
	if err != nil {
		return fmt.Errorf("s3: abort upload %s: %w", key, err)
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return readError("abort upload", key, resp)
	}

	drain(resp)

	return nil
}
