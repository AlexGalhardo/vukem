package mail

import (
	"strconv"

	"github.com/AlexGalhardo/vukem/mail"
)

// The finance mailables are React Email components in resources/js/emails,
// exported to resources/emails (npm run build:emails). The fields below are
// what the components read as Go template actions (.Name, .URL...); the
// Tags let the Resend webhook tie a bounce back to the kind of mail.

// Welcome greets a new user and points at the app.
type Welcome struct {
	To, Name, Workspace, URL string
}

// Envelope implements mail.Mailable.
func (m Welcome) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.To}, Subject: "Welcome to " + m.Workspace, Tags: tag("welcome")}
}

// Content implements mail.Mailable.
func (m Welcome) Content() mail.Content { return mail.React("welcome", m) }

// WorkspaceInvite carries the link that joins a workspace.
type WorkspaceInvite struct {
	To, Inviter, Workspace, Role, URL string
}

// Envelope implements mail.Mailable.
func (m WorkspaceInvite) Envelope() mail.Envelope {
	return mail.Envelope{
		To: []string{m.To}, Subject: m.Inviter + " invited you to " + m.Workspace, Tags: tag("workspace_invite"),
	}
}

// Content implements mail.Mailable.
func (m WorkspaceInvite) Content() mail.Content { return mail.React("workspace_invite", m) }

// TrialEnding warns that the Pro trial ends soon.
type TrialEnding struct {
	To, Workspace, URL string
}

// Envelope implements mail.Mailable.
func (m TrialEnding) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.To}, Subject: "Your Pro trial is ending", Tags: tag("trial_ending")}
}

// Content implements mail.Mailable.
func (m TrialEnding) Content() mail.Content { return mail.React("trial_ending", m) }

// PaymentFailed asks the owner to fix the payment method.
type PaymentFailed struct {
	To, Workspace, URL string
}

// Envelope implements mail.Mailable.
func (m PaymentFailed) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.To}, Subject: "Your payment failed", Tags: tag("payment_failed")}
}

// Content implements mail.Mailable.
func (m PaymentFailed) Content() mail.Content { return mail.React("payment_failed", m) }

// Receipt confirms a paid invoice. Amount is formatted ("$9.00").
type Receipt struct {
	To, Workspace, Amount, URL string
}

// Envelope implements mail.Mailable.
func (m Receipt) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.To}, Subject: "Your receipt for " + m.Amount, Tags: tag("receipt")}
}

// Content implements mail.Mailable.
func (m Receipt) Content() mail.Content { return mail.React("receipt", m) }

// BudgetAlert says a category reached 80% or 100% of its monthly budget.
type BudgetAlert struct {
	To, Workspace, Category, Spent, Limit, URL string
	Percent                                    int
}

// Envelope implements mail.Mailable.
func (m BudgetAlert) Envelope() mail.Envelope {
	return mail.Envelope{
		To:      []string{m.To},
		Subject: m.Category + " budget at " + strconv.Itoa(m.Percent) + "%",
		Tags:    tag("budget_alert"),
	}
}

// Content implements mail.Mailable.
func (m BudgetAlert) Content() mail.Content { return mail.React("budget_alert", m) }

// Line is one row of the monthly summary: where the money went.
type Line struct {
	Category, Amount string
}

// MonthlySummary is the report of the month that just ended.
type MonthlySummary struct {
	To, Workspace, Month, Income, Expense, Net, URL string
	Top                                             []Line
}

// Envelope implements mail.Mailable.
func (m MonthlySummary) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{m.To}, Subject: "Your " + m.Month + " summary", Tags: tag("monthly_summary")}
}

// Content implements mail.Mailable.
func (m MonthlySummary) Content() mail.Content { return mail.React("monthly_summary", m) }

func tag(kind string) map[string]string { return map[string]string{"type": kind} }
