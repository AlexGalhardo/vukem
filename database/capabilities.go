package database

// Capabilities describes what a dialect's SQL supports, so callers branch
// on behaviour instead of on Dialect.Name().
type Capabilities struct {
	// Family is the SQL grammar: "sqlite", "postgres" or "mysql". DDL types
	// and the SQL lexer follow it.
	Family string
	// Returning reports INSERT/UPDATE/DELETE ... RETURNING.
	Returning bool
	// MaxParams is the bound-parameter limit of one statement.
	MaxParams int
	// RowLocks reports SELECT ... FOR UPDATE / FOR SHARE.
	RowLocks bool
	// ILike reports a native ILIKE operator.
	ILike bool
	// LimitForOffset is the LIMIT a bare OFFSET needs ("" when none).
	LimitForOffset string
	// TransactionalDDL reports that DDL rolls back with its transaction.
	TransactionalDDL bool
	// AdvisoryLock is "postgres", "mysql" or "" (no session lock).
	AdvisoryLock string
	// PartialIndexes reports CREATE INDEX ... WHERE.
	PartialIndexes bool
	// IndexMethods reports CREATE INDEX ... USING.
	IndexMethods bool
	// AlterConstraints reports ADD/DROP CHECK and ADD FOREIGN KEY on an
	// existing table.
	AlterConstraints bool
}

// Capable is implemented by dialects that describe themselves.
type Capable interface{ Capabilities() Capabilities }

// CapabilitiesOf returns d's capabilities. The second result is false for
// a dialect that neither is built in nor implements Capable.
func CapabilitiesOf(d Dialect) (Capabilities, bool) {
	if c, ok := d.(Capable); ok {
		return c.Capabilities(), true
	}

	return Capabilities{}, false
}

// MustCapabilities returns d's capabilities, or Capabilities{} with Family
// "" for an unknown dialect. Open rejects such a dialect, so callers reached
// through a *DB never see the zero value.
func MustCapabilities(d Dialect) Capabilities {
	c, _ := CapabilitiesOf(d)
	return c
}

// Capabilities implements Capable.
func (sqliteDialect) Capabilities() Capabilities {
	return Capabilities{
		Family: "sqlite", Returning: true, MaxParams: 32766, LimitForOffset: "-1",
		TransactionalDDL: true, PartialIndexes: true,
	}
}

// Capabilities implements Capable.
func (postgresDialect) Capabilities() Capabilities {
	return Capabilities{
		Family: "postgres", Returning: true, MaxParams: 65535, RowLocks: true, ILike: true,
		TransactionalDDL: true, AdvisoryLock: "postgres", PartialIndexes: true, IndexMethods: true,
		AlterConstraints: true,
	}
}

// Capabilities implements Capable.
func (mysqlDialect) Capabilities() Capabilities {
	return Capabilities{
		Family: "mysql", MaxParams: 65535, RowLocks: true, LimitForOffset: "18446744073709551615",
		AdvisoryLock: "mysql", AlterConstraints: true,
	}
}
