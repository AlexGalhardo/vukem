# QR codes

`contrib/qrcode` (`github.com/AlexGalhardo/vukem/contrib/qrcode`) encodes a short string as a PNG or an SVG QR code. It is its own module. Core does not import it, and it does not import Vukem, so it needs no Vukem version pin.

```bash
go get github.com/AlexGalhardo/vukem/contrib/qrcode
```

The encoder is [`rsc.io/qr` v0.2.0](https://pkg.go.dev/rsc.io/qr). The standard library has no QR encoder, and this module does not implement Reed-Solomon. SVG is drawn here from `Code.Black`. There is no second QR library. `(*qr.Code).PNG` is not used: it treats pixel coordinates as modules and fixes the quiet zone and the scale at 8.

The same content and options produce the same bytes. PNG bytes are stable for one Go toolchain (`image/png`). SVG bytes are stable across toolchains.

## Options

| Field | Meaning |
| --- | --- |
| `Level` | `Low`, `Medium`, `Quartile`, `High` (about 7%, 15%, 25%, 30% recovery). The zero value is `Low`. Any other value is `ErrInvalidOptions`. |
| `Scale` | Module size in pixels. `0` means 8. Accepted range is 1..32. |
| `Quiet` | Quiet zone in modules, as a `*int`. `nil` means 4. `0` is a real width. Accepted range is 0..16. |

Empty content is `ErrEmpty`. Content longer than 2048 bytes (`MaxContent`, counted in bytes, not runes) is `ErrTooLarge` and is rejected before encode.

## Drawing

```go
png, err := qrcode.PNG("https://example.com", qrcode.Options{})
svg, err := qrcode.SVG("https://example.com", qrcode.Options{Level: qrcode.Medium, Scale: 4})
```

`PNG` returns a paletted image (white index 0, black index 1) encoded with `png.DefaultCompression`.

`SVG` is one white `<rect>` and one black `<path>`. Each black module is a subpath `M{x} {y}h{scale}v{scale}h-{scale}z`, in row-major order, with `shape-rendering="crispEdges"`. The document uses LF line endings and does not embed the payload.

## HTTP

`Handler` is an `http.Handler` for GET and HEAD. The payload is the `data` query parameter. The response is `image/png` with `Content-Length`. HEAD sends the headers and no body.

| Case | Status |
| --- | --- |
| Missing or empty `data` | 400 |
| Payload that fails validation or cannot be encoded | 400, and the payload is not echoed |
| Method other than GET or HEAD | 405, `Allow: GET, HEAD` |
| Invalid `Options` (the caller's configuration) | 500 |

Mount it on any `http.ServeMux`. This page does not wire it into an application.

```go
mux.Handle("/qr", qrcode.Handler(qrcode.Options{}))
// GET /qr?data=https%3A%2F%2Fexample.com
```
