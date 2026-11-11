package openapi_test

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/openapi"
)

func TestTypeScriptClientThrowGolden(t *testing.T) {
	t.Parallel()

	got, err := openapi.TypeScriptClientWith(
		openapi.Generate(routes(), openapi.Info{PathPrefix: "/api"}),
		openapi.ClientOptions{Style: openapi.StyleThrow, Args: "-style throw"},
	)
	if err != nil {
		t.Fatal(err)
	}

	checkGolden(t, "shop.client.throw.ts", got)

	if !strings.Contains(got, "throw new ApiError") || strings.Contains(got, "ApiResult") {
		t.Error("the throw style is not the style that throws")
	}
}

func TestTypeScriptClientTanStackGolden(t *testing.T) {
	t.Parallel()

	got, err := openapi.TypeScriptClientWith(
		openapi.Generate(routes(), openapi.Info{PathPrefix: "/api"}),
		openapi.ClientOptions{TanStack: true, Args: "-tanstack"},
	)
	if err != nil {
		t.Fatal(err)
	}

	checkGolden(t, "shop.client.tanstack.ts", got)

	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasPrefix(line, "import ") {
			t.Errorf("the TanStack helpers import something (%q): the client must have no dependency", line)
		}
	}
}

func TestTypeScriptClientRejectsBadOptions(t *testing.T) {
	t.Parallel()

	doc := openapi.Generate(routes(), openapi.Info{})

	if _, err := openapi.TypeScriptClientWith(
		doc,
		openapi.ClientOptions{Style: openapi.StyleThrow, TanStack: true},
	); err == nil {
		t.Error("TanStack with the throw style was accepted")
	}

	if _, err := openapi.TypeScriptClientWith(doc, openapi.ClientOptions{Style: "promise"}); err == nil {
		t.Error("an unknown style was accepted")
	}
}

// The union of errors of an operation comes from the responses the document
// declares: one member for each status, then every other status.
func TestTypeScriptClientErrorUnions(t *testing.T) {
	t.Parallel()

	got, err := openapi.TypeScriptClient(openapi.Generate(routes(), openapi.Info{PathPrefix: "/api"}))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		// products.store declares 401 and 422.
		"export type ProductsStoreError =\n  | { status: 401; value: Problem }\n  | { status: 422; value: Problem }\n" +
			"  | { status: Exclude<HttpErrorStatus, 401 | 422>; value: ApiProblem | undefined };",
		// products.destroy declares no error: only the generic member.
		"export type ProductsDestroyError =\n  | { status: HttpErrorStatus; value: ApiProblem | undefined };",
		// A 204 has no data.
		"Promise<ApiResult<ProductsDestroyResponse, ProductsDestroyError>>",
		"throwHttpError?: boolean;",
		"onRequest?:",
		"signal?: AbortSignal;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("client lacks %q", want)
		}
	}
}
