package vite_test

import (
	"fmt"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/vite"
)

func ExampleVite_Tags() {
	// Apps pass os.DirFS("public"); `vite build` writes the manifest there.
	public := fstest.MapFS{
		"build/.vite/manifest.json": {Data: []byte(`{
  "resources/js/app.ts": {"file": "assets/app-1.js", "src": "resources/js/app.ts", "isEntry": true}
}`)},
	}
	v, err := vite.New(vite.Config{FS: public})
	if err != nil {
		fmt.Println(err)
		return
	}

	// Views call {{vite "resources/js/app.ts"}}, which passes the CSP nonce.
	tags, _ := v.Tags("n0nce", "resources/js/app.ts")
	fmt.Println(tags)
	// Output: <script type="module" src="/build/assets/app-1.js" nonce="n0nce"></script>
}

func ExampleVite_Tags_dev() {
	// In development the Vite plugin writes public/hot with the server URL.
	public := fstest.MapFS{"hot": {Data: []byte("http://localhost:5173")}}
	v, _ := vite.New(vite.Config{FS: public, Dev: true})

	tags, _ := v.Tags("n0nce", "resources/js/app.ts")
	fmt.Println(v.HotURL())
	fmt.Println(tags)
	// Output:
	// http://localhost:5173
	// <script type="module" src="http://localhost:5173/@vite/client" nonce="n0nce"></script>
	// <script type="module" src="http://localhost:5173/resources/js/app.ts" nonce="n0nce"></script>
}
