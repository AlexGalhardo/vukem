package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

// patch rewrites old into new in a user-owned file that install:auth wrote
// earlier. marker tells that the file was already patched.
type patch struct {
	file, old, new, marker string
	// optional patches only print a hint when the file was changed by hand.
	optional bool
}

// loginTwoFactorPatch makes Login stop at the code step for users with
// two-factor authentication (same text in the HTMX and Inertia scaffolds).
var loginTwoFactorPatch = patch{
	file: "app/controllers/account/account.go",
	old: `	if _, err := h.a.Web.Attempt(c, creds, auth.Remember(in.Remember)); err != nil {
		return err
	}
`,
	new: `
	u, err := h.a.Web.Validate(c, creds)
	if err != nil {
		return err
	}

	// Users with two-factor authentication enter a code first (vukem install:auth --2fa).
	if pending, err := h.startTwoFactor(c, u, in.Remember); pending || err != nil {
		return err
	}

	if err := h.a.Web.Login(c, u, auth.Remember(in.Remember)); err != nil {
		return err
	}
`,
	marker: "h.startTwoFactor(",
}

// socialTwoFactorPatch makes social sign-in stop at the code step too, so a
// provider account alone cannot bypass the second factor.
var socialTwoFactorPatch = patch{
	file: "routes/social.go",
	old: `		if err := acc.Web.Login(c, u); err != nil {
			return err
		}
`,
	new: `		// Users with two-factor authentication enter a code first (vukem install:auth --2fa).
		pending, err := acc.StartTwoFactorChallenge(c, u, false)
		if err != nil {
			return err
		}

		if pending {
			return c.Redirect("/two-factor-challenge")
		}

		if err := acc.Web.Login(c, u); err != nil {
			return err
		}
`,
	marker: "StartTwoFactorChallenge",
}

const dashboardLinkClass = `"text-indigo-600 hover:underline dark:text-indigo-400"`

// dashboardTwoFactorPatches link the settings page from the dashboard of
// each frontend; only the project's own dashboard exists.
var dashboardTwoFactorPatches = []patch{
	{
		file: "resources/views/auth/dashboard.html",
		old:  `<form method="post" action="{{route "logout"}}">`,
		new: `<p><a href="{{route "two-factor.show"}}">Two-factor authentication</a></p>
<form method="post" action="{{route "logout"}}">`,
		marker: "two-factor.show", optional: true,
	},
	{
		file: "resources/js/pages/Dashboard.tsx",
		old:  "      )}\n    </>\n",
		new: "      )}\n      <p className=\"mt-2\">\n        <Link href=\"/user/two-factor\" className=" + dashboardLinkClass +
			">\n          Two-factor authentication\n        </Link>\n      </p>\n    </>\n",
		marker:   "/user/two-factor",
		optional: true,
	},
	{
		file: "resources/js/pages/Dashboard.vue",
		old:  "  </p>\n</template>\n",
		new: "  </p>\n  <p class=\"mt-2\">\n    <Link href=\"/user/two-factor\" class=" + dashboardLinkClass +
			">Two-factor authentication</Link>\n  </p>\n</template>\n",
		marker: "/user/two-factor", optional: true,
	},
	{
		file: "resources/js/pages/Dashboard.svelte",
		old:  "{/if}\n",
		new: "{/if}\n<p class=\"mt-2\">\n  <Link href=\"/user/two-factor\" class=" + dashboardLinkClass +
			">Two-factor authentication</Link>\n</p>\n",
		marker: "/user/two-factor", optional: true,
	},
}

// installTwoFactor adds two-factor authentication (install:auth --2fa) to
// a project that has the authentication scaffold.
func installTwoFactor(env *Env, data map[string]string, force bool) ([]registration, error) {
	// Without the login change 2FA could be turned on but never asked
	// for: refuse before writing anything (fail closed).
	if err := checkPatches(env, loginTwoFactorPatch, socialTwoFactorPatch); err != nil {
		return nil, err
	}

	names := []string{"auth-2fa", "auth-2fa-web"}
	if fw := inertiaFramework(env); fw != "" {
		names = []string{"auth-2fa", "auth-2fa-inertia", "auth-2fa-inertia-" + fw}
	}

	for _, name := range names {
		if err := installScaffold(env, name, data, force); err != nil {
			return nil, err
		}
	}

	if err := applyPatches(env, append([]patch{loginTwoFactorPatch}, dashboardTwoFactorPatches...)...); err != nil {
		return nil, err
	}

	return []registration{
		{"database/migrations/registry.go", "All", "m" + data["TS4"] + "AddTwoFactorColumns()"},
		{"routes/registry.go", "All", "TwoFactor"},
	}, nil
}

// checkPatches fails when an existing file has neither the text a
// required patch replaces nor the patched text.
func checkPatches(env *Env, patches ...patch) error {
	for _, p := range patches {
		src, err := os.ReadFile(env.path(filepath.FromSlash(p.file)))
		if os.IsNotExist(err) {
			if p.file == loginTwoFactorPatch.file {
				return usagef("no %s: run vukem install:auth first", p.file)
			}

			continue
		}

		if err != nil {
			return err
		}

		if !p.optional && !bytes.Contains(src, []byte(p.marker)) && !bytes.Contains(src, p.oldIn(src)) {
			return fmt.Errorf("%s was changed by hand, so install:auth --2fa cannot update it; "+
				"replace\n\n%s\nwith\n\n%s", p.file, p.old, p.new)
		}
	}

	return nil
}

// applyPatches applies patches to the files that exist and are not
// patched yet. Files with CRLF line endings keep them.
func applyPatches(env *Env, patches ...patch) error {
	if err := checkPatches(env, patches...); err != nil {
		return err
	}

	for _, p := range patches {
		path := env.path(filepath.FromSlash(p.file))

		src, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}

		if err != nil {
			return err
		}

		if bytes.Contains(src, []byte(p.marker)) {
			continue
		}

		old := p.oldIn(src)
		if !bytes.Contains(src, old) {
			fmt.Fprintf(env.Stdout, "could not update %s automatically; add a link to /user/two-factor\n", p.file)
			continue
		}

		repl := []byte(p.new)
		if bytes.Contains(src, []byte("\r\n")) {
			repl = bytes.ReplaceAll(repl, []byte("\n"), []byte("\r\n"))
		}

		if err := writeFile(path, bytes.Replace(src, old, repl, 1)); err != nil {
			return err
		}

		fmt.Fprintln(env.Stdout, "updated", p.file)
	}

	return nil
}

// oldIn returns p.old with the line endings of src.
func (p patch) oldIn(src []byte) []byte {
	if bytes.Contains(src, []byte("\r\n")) {
		return bytes.ReplaceAll([]byte(p.old), []byte("\n"), []byte("\r\n"))
	}

	return []byte(p.old)
}
