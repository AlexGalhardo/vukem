package requests

import "github.com/AlexGalhardo/vukem/z"

// The finance forms. Ids and amounts arrive as text (select values, "12.50")
// and are parsed by the finance service, which owns the money rules.

// Transaction is the input of the new transaction form.
type Transaction struct {
	AccountID   string `json:"accountId"   form:"accountId"`
	CategoryID  string `json:"categoryId"  form:"categoryId"`
	Kind        string `json:"kind"        form:"kind"`
	Amount      string `json:"amount"      form:"amount"`
	Description string `json:"description" form:"description"`
	Date        string `json:"date"        form:"date"`
}

// TransactionSchema validates Transaction.
var TransactionSchema = z.Struct[Transaction](z.Shape{
	"accountId":   z.String().Trim().Min(1).Max(20),
	"categoryId":  z.String().Trim().Max(20).Optional(),
	"kind":        z.String().Trim().Min(1).Max(20),
	"amount":      z.String().Trim().Min(1).Max(30),
	"description": z.String().Trim().Max(200).Optional(),
	"date":        z.String().Trim().Min(10).Max(10),
})

// Account is the input of the new account form.
type Account struct {
	Name    string `json:"name"    form:"name"`
	Kind    string `json:"kind"    form:"kind"`
	Opening string `json:"opening" form:"opening"`
}

// AccountSchema validates Account.
var AccountSchema = z.Struct[Account](z.Shape{
	"name":    z.String().Trim().Min(1).Max(80),
	"kind":    z.String().Trim().Min(1).Max(20),
	"opening": z.String().Trim().Max(30).Optional(),
})

// Category is the input of the new category form.
type Category struct {
	Name string `json:"name" form:"name"`
	Kind string `json:"kind" form:"kind"`
}

// CategorySchema validates Category.
var CategorySchema = z.Struct[Category](z.Shape{
	"name": z.String().Trim().Min(1).Max(60),
	"kind": z.String().Trim().Min(1).Max(20),
})

// Budget is the input of the budget form.
type Budget struct {
	CategoryID string `json:"categoryId" form:"categoryId"`
	Limit      string `json:"limit"      form:"limit"`
}

// BudgetSchema validates Budget.
var BudgetSchema = z.Struct[Budget](z.Shape{
	"categoryId": z.String().Trim().Min(1).Max(20),
	"limit":      z.String().Trim().Min(1).Max(30),
})

// Invite is the input of the invite form.
type Invite struct {
	Email string `json:"email" form:"email"`
	Role  string `json:"role"  form:"role"`
}

// InviteSchema validates Invite.
var InviteSchema = z.Struct[Invite](z.Shape{
	"email": z.String().Trim().Min(3).Max(254),
	"role":  z.String().Trim().Min(1).Max(20),
})

// Workspace is the input of the workspace settings form.
type Workspace struct {
	Name     string `json:"name"     form:"name"`
	Currency string `json:"currency" form:"currency"`
}

// WorkspaceSchema validates Workspace.
var WorkspaceSchema = z.Struct[Workspace](z.Shape{
	"name":     z.String().Trim().Min(1).Max(80),
	"currency": z.String().Trim().Min(3).Max(3),
})

// ImportMapping is the input of the column mapping step of a CSV import.
type ImportMapping struct {
	Date        string `json:"date"        form:"date"`
	Description string `json:"description" form:"description"`
	Amount      string `json:"amount"      form:"amount"`
	Category    string `json:"category"    form:"category"`
	DateFormat  string `json:"dateFormat"  form:"dateFormat"`
	Invert      string `json:"invert"      form:"invert"`
}

// ImportMappingSchema validates ImportMapping.
var ImportMappingSchema = z.Struct[ImportMapping](z.Shape{
	"date":        z.String().Trim().Min(1).Max(120),
	"description": z.String().Trim().Min(1).Max(120),
	"amount":      z.String().Trim().Min(1).Max(120),
	"category":    z.String().Trim().Max(120).Optional(),
	"dateFormat":  z.String().Trim().Max(20).Optional(),
	"invert":      z.String().Trim().Max(5).Optional(),
})
