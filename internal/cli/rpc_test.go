package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestMakeProto(t *testing.T) {
	t.Parallel()

	dir := project(t)

	code, out, errOut := vukem(t, dir, "make:proto", "BlogPostService")
	if code != 0 {
		t.Fatalf("make:proto = %d\n%s%s", code, out, errOut)
	}

	for _, want := range []string{"created proto/blog_post/v1/blog_post.proto", "created buf.yaml", "created buf.gen.yaml", "go get -tool " + protocGenGo + "@" + protobufVersion, "vukem codegen:rpc"} {
		if !strings.Contains(out, want) {
			t.Errorf("make:proto output lacks %q:\n%s", want, out)
		}
	}

	for _, rel := range []string{"proto/blog_post/v1/blog_post.proto", "buf.yaml", "buf.gen.yaml"} {
		golden(t, "rpc/"+rel, read(t, dir, rel))
	}

	if code, _, errOut := vukem(
		t,
		dir,
		"make:proto",
		"BlogPost",
	); code == 0 ||
		!strings.Contains(errOut, "already exists") {
		t.Errorf("make:proto twice = %d %q, want already exists", code, errOut)
	}

	custom := "version: v2\n# mine\n"
	if err := os.WriteFile(filepath.Join(dir, "buf.gen.yaml"), []byte(custom), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, out, errOut := vukem(t, dir, "make:proto", "Invoice"); code != 0 || strings.Contains(out, "created buf") {
		t.Fatalf("make:proto Invoice = %d\n%s%s", code, out, errOut)
	}

	if got := read(t, dir, "buf.gen.yaml"); got != custom {
		t.Errorf("make:proto overwrote buf.gen.yaml: %q", got)
	}

	if got := read(
		t,
		dir,
		"proto/invoice/v1/invoice.proto",
	); !strings.Contains(
		got,
		`go_package = "example.com/blog/gen/invoice/v1;invoicev1"`,
	) {
		t.Errorf("invoice.proto go_package:\n%s", got)
	}
}

func TestTools(t *testing.T) {
	t.Parallel()

	gomod := []byte("module x\r\n\r\ngo 1.27.0\r\n\r\ntool github.com/bufbuild/buf/cmd/buf\r\n\r\ntool (\r\n\t" +
		protocGenGo + "\r\n\t// comment\r\n\t" + protocGenConnectGo + "\r\n)\r\n\r\nrequire (\r\n\tfoo v1.0.0\r\n)\r\n")

	if diff := cmp.Diff([]string{bufTool, protocGenGo, protocGenConnectGo}, tools(gomod)); diff != "" {
		t.Errorf("tools() (-want +got):\n%s", diff)
	}

	if got := missingTools(
		[]byte("module x\n\ntool "+protocGenGo+"\n"),
		protocGenGo,
		protocGenConnectGo,
	); !cmp.Equal(
		got,
		[]string{protocGenConnectGo},
	) {
		t.Errorf("missingTools() = %v", got)
	}
}

func TestCodegenRPC_NeedsTheGeneratorTools(t *testing.T) {
	t.Parallel()

	dir := project(t)

	code, _, errOut := vukem(t, dir, "codegen:rpc")
	if code != 1 {
		t.Fatalf("codegen:rpc = %d, want 1", code)
	}

	for _, want := range []string{
		"go get -tool " + protocGenGo + "@" + protobufVersion,
		"go get -tool " + protocGenConnectGo + "@" + connectVersion,
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("codegen:rpc error lacks %q:\n%s", want, errOut)
		}
	}

	if code, _, _ := vukem(t, dir, "codegen:rpc", "extra"); code != 2 {
		t.Errorf("codegen:rpc extra = %d, want usage error", code)
	}
}

const rpcService = `package rpc

import (
	"context"

	"connectrpc.com/connect"

	blogpostv1 "example.com/blog/gen/blog_post/v1"
	"example.com/blog/gen/blog_post/v1/blogpostv1connect"
	"github.com/AlexGalhardo/vukem/errs"
)

type BlogPostService struct {
	blogpostv1connect.UnimplementedBlogPostServiceHandler
}

func (BlogPostService) GetBlogPost(
	_ context.Context,
	req *connect.Request[blogpostv1.GetBlogPostRequest],
) (*connect.Response[blogpostv1.GetBlogPostResponse], error) {
	if req.Msg.GetId() == 0 {
		return nil, errs.ErrNotFound
	}

	return connect.NewResponse(&blogpostv1.GetBlogPostResponse{Id: req.Msg.GetId(), Name: "Hello"}), nil
}
`

const rpcServiceTest = `package rpc

import (
	"testing"

	"connectrpc.com/connect"

	blogpostv1 "example.com/blog/gen/blog_post/v1"
	"example.com/blog/gen/blog_post/v1/blogpostv1connect"
	"github.com/AlexGalhardo/vukem/contrib/grpc"
	"github.com/AlexGalhardo/vukem/contrib/grpc/grpctest"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func TestBlogPostService(t *testing.T) {
	r := router.New(&web.Services{})
	path, h := blogpostv1connect.NewBlogPostServiceHandler(BlogPostService{}, grpc.HandlerOptions(grpc.Options{})...)
	grpc.Mount(r, path, h)

	handler, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	srv := grpctest.NewServer(t, handler)

	for _, p := range grpctest.Protocols() {
		client := blogpostv1connect.NewBlogPostServiceClient(srv.Client, srv.URL, p.Options...)

		res, err := client.GetBlogPost(t.Context(), connect.NewRequest(&blogpostv1.GetBlogPostRequest{Id: 7}))
		if err != nil || res.Msg.GetName() != "Hello" {
			t.Errorf("%s: GetBlogPost(7) = %v, %v", p.Name, res, err)
		}

		_, err = client.GetBlogPost(t.Context(), connect.NewRequest(&blogpostv1.GetBlogPostRequest{}))
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Errorf("%s: GetBlogPost(0) = %v, want not_found", p.Name, err)
		}
	}
}
`

// TestCodegenRPC_GeneratedServiceCompilesAndPasses runs make:proto and
// codegen:rpc in a new app, then builds and tests a service on the
// generated code. It needs buf or protoc on PATH (the plugins come from
// the module cache or the network through go tool).
func TestCodegenRPC_GeneratedServiceCompilesAndPasses(t *testing.T) {
	t.Parallel()
	requireGo(t)

	_, bufErr := exec.LookPath("buf")
	_, protocErr := exec.LookPath("protoc")

	if bufErr != nil && protocErr != nil {
		t.Skip("codegen:rpc e2e needs buf or protoc on PATH (go install " + bufTool + "@" + bufVersion + ")")
	}

	dir := project(t)
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	gomod := read(t, dir, "go.mod") + "\nrequire github.com/AlexGalhardo/vukem/contrib/grpc v0.0.0\n\n" +
		"replace github.com/AlexGalhardo/vukem/contrib/grpc => " + filepath.ToSlash(filepath.Join(repo, "contrib", "grpc")) + "\n"

	files := map[string]string{
		"go.mod":                    gomod,
		"app/rpc/blog_post.go":      rpcService,
		"app/rpc/blog_post_test.go": rpcServiceTest,
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	goCmd := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")

		if out, err := combinedOutput(cmd); err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
	}

	if code, out, errOut := vukem(t, dir, "make:proto", "BlogPost"); code != 0 {
		t.Fatalf("make:proto = %d\n%s%s", code, out, errOut)
	}

	goCmd("get", "-tool", protocGenGo+"@"+protobufVersion, protocGenConnectGo+"@"+connectVersion)

	args := []string{"codegen:rpc"}
	if bufErr != nil {
		args = append(args, "--protoc")
	}

	if code, out, errOut := vukem(t, dir, args...); code != 0 {
		t.Fatalf("codegen:rpc = %d\n%s%s", code, out, errOut)
	}

	for _, rel := range []string{"gen/blog_post/v1/blog_post.pb.go", "gen/blog_post/v1/blogpostv1connect/blog_post.connect.go"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("codegen:rpc did not write %s: %v", rel, err)
		}
	}

	goCmd("mod", "tidy")
	goCmd("vet", "./...")
	goCmd("test", "-count=1", "./app/rpc/...")
}
