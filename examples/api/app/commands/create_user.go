// Package commands holds the application's own CLI commands.
package commands

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/examples/api/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/api/app/models"
	"github.com/AlexGalhardo/vukem/orm"
)

// CreateUser is "user:create": it adds an account, with a verified email,
// that can request tokens.
// The password is read from standard input so it stays out of shell history:
//
//	echo "correct horse battery" | vukem user:create -name Ada ada@example.com
func CreateUser() vukem.Command {
	var name *string

	return vukem.CommandFunc{
		CommandName: "user:create",
		Desc:        "Create a user (password on stdin): vukem user:create -name Ada ada@example.com",
		DefineFlags: func(fs *flag.FlagSet) { name = fs.String("name", "API User", "display name") },
		Fn: func(ctx context.Context, app *vukem.App, args []string) error {
			if len(args) != 1 {
				return errors.New("user:create: want one email argument")
			}

			password, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil && password == "" {
				return fmt.Errorf("user:create: read password from stdin: %w", err)
			}

			// Windows PowerShell pipes text with a UTF-8 BOM.
			password = strings.TrimRight(strings.TrimPrefix(password, "\ufeff"), "\r\n")
			if len(password) < 12 {
				return errors.New("user:create: the password needs at least 12 characters")
			}

			acc, err := accounts.New(app)
			if err != nil {
				return err
			}

			hash, err := acc.Hasher.Make(password)
			if err != nil {
				return err
			}

			// The operator vouches for the address: tokens need a verified one.
			u := models.User{Name: *name, Email: strings.ToLower(args[0]), Password: hash}
			u.EmailVerifiedAt.V, u.EmailVerifiedAt.Valid = time.Now().UTC(), true
			if err := orm.Create(ctx, acc.DB, &u); err != nil {
				return fmt.Errorf("user:create: %w", err)
			}

			fmt.Fprintf(os.Stdout, "created user %d <%s>\n", u.ID, u.Email)

			return nil
		},
	}
}
