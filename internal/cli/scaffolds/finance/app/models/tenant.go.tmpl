package models

import (
	"context"
	"strconv"

	"github.com/AlexGalhardo/vukem/tenancy"
)

// This file is where the models meet the tenancy package: every domain
// model takes its workspace_id from the context tenant on insert, whatever
// the request sent, and fails without one (tenancy.ErrNoTenant).
// app/workspaces/tenant.go holds the request side.

func tenantID(id tenancy.ID) int64 {
	n, _ := strconv.ParseInt(string(id), 10, 64)
	return n
}

// SetTenant implements tenancy.Scoped.
func (m *Account) SetTenant(id tenancy.ID) { m.WorkspaceID = tenantID(id) }

// BeforeCreate stamps the tenant.
func (m *Account) BeforeCreate(ctx context.Context) error { return tenancy.Assign(ctx, m) }

// SetTenant implements tenancy.Scoped.
func (m *Category) SetTenant(id tenancy.ID) { m.WorkspaceID = tenantID(id) }

// BeforeCreate stamps the tenant.
func (m *Category) BeforeCreate(ctx context.Context) error { return tenancy.Assign(ctx, m) }

// SetTenant implements tenancy.Scoped.
func (m *Transaction) SetTenant(id tenancy.ID) { m.WorkspaceID = tenantID(id) }

// BeforeCreate stamps the tenant.
func (m *Transaction) BeforeCreate(ctx context.Context) error { return tenancy.Assign(ctx, m) }

// SetTenant implements tenancy.Scoped.
func (m *Budget) SetTenant(id tenancy.ID) { m.WorkspaceID = tenantID(id) }

// BeforeCreate stamps the tenant.
func (m *Budget) BeforeCreate(ctx context.Context) error { return tenancy.Assign(ctx, m) }

// SetTenant implements tenancy.Scoped.
func (m *Import) SetTenant(id tenancy.ID) { m.WorkspaceID = tenantID(id) }

// BeforeCreate stamps the tenant.
func (m *Import) BeforeCreate(ctx context.Context) error { return tenancy.Assign(ctx, m) }

// SetTenant implements tenancy.Scoped.
func (m *Invite) SetTenant(id tenancy.ID) { m.WorkspaceID = tenantID(id) }

// BeforeCreate stamps the tenant.
func (m *Invite) BeforeCreate(ctx context.Context) error { return tenancy.Assign(ctx, m) }
