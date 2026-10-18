package cli

import (
	"fmt"
	"strings"
)

// docs is the documentation site the generated project is pointed at.
const docs = "https://vukem.dev/docs"

// nextSteps is the handover: every step a new project needs before it is
// deployed, each with the page that explains it. The README lists every
// documentation section; this list is the order to read them in.
func nextSteps(name, desc, kind string, o standardsOpts) string {
	type step struct{ do, doc string }

	steps := []step{
		{"cd " + name, ""},
		{"read .env: APP_KEY is set; APP_URL, the database and mail live here", docs + "/configuration"},
	}

	if kind != "minimal" {
		steps = append(steps,
			step{
				"pick a database: SQLite is the default, and PostgreSQL, MySQL or MongoDB\n" +
					"     need their driver in bootstrap/app.go",
				docs + "/database/overview",
			},
			step{"apply the migrations: vukem migrate", docs + "/database/migrations"},
		)
	}

	steps = append(steps,
		step{"run the tests: go test ./...", docs + "/testing/overview"},
		step{
			"set MAIL_MAILER and MAIL_FROM, or verification and password reset\n     emails have nowhere to go",
			docs + "/digging-deeper/mail",
		},
	)

	if o.docker {
		steps = append(steps, step{
			"or run the whole stack with the mail catcher: docker compose up --build",
			docs + "/deployment/docker-and-railway",
		})
	}

	steps = append(steps,
		step{"start the app: vukem serve, then open http://localhost:3000", docs + "/cli/overview"},
		step{"deploy the Dockerfile, or vukem build for a single binary", docs + "/deployment/docker-and-railway"},
	)

	var b strings.Builder

	fmt.Fprintf(&b, "\nCreated %s (%s). Next:\n\n", name, desc)

	for i, s := range steps {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, s.do)

		if s.doc != "" {
			fmt.Fprintf(&b, "     %s\n", s.doc)
		}
	}

	fmt.Fprintf(&b, "\nEvery documentation section is linked from README.md: %s\n", docs)

	if !o.standards || o.license == "" || !o.ci || o.deploy == "" || !o.docker {
		fmt.Fprint(&b, "Project standards you did not ask for: vukem help new lists --standards,\n"+
			"--license, --ci, --deploy and --docker.\n")
	}

	b.WriteByte('\n')

	return b.String()
}
