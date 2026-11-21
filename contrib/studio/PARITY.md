# Parity ledger

The old server-rendered pages were removed in favour of the React interface. This ledger
lists every capability the old pages had, and the tests that cover it now, so
that deleting the old pages and their tests lost no coverage. `go run
./scripts/paritycheck PARITY.md` (from `contrib/studio`) fails when a row names
no test, when a named test does not exist, or when a named Go test does not
pass. The Playwright and bun tests are checked to exist, and run by their own
commands (`bunx playwright test`, `bun test src`); `-e2e` and `-bun` run the
files a ledger names.

Reference kinds: `go:` a Go test of the root package, `gocmd:` one under
`cmd/`, `e2e:file#title` a Playwright test, `bun:path#title` a bun test.

| Area | Capability of the old pages | Covered by |
| --- | --- | --- |
| Shell | Overview of the sources, with no credentials in it | `go:TestBootstrapListsSourcesWithoutCredentials`; `e2e:studio.spec.ts#opens the token URL and lists the sources` |
| Shell | Read-only and write badge | `e2e:studio.spec.ts#the badge says read-only`; `e2e:studio.spec.ts#the badge says write enabled with --write` |
| Shell | Sidebar with the tables, collections and keys of a source | `e2e:studio.spec.ts#opens a source and lists its tables with row estimates`; `e2e:mongo-read.spec.ts#the collections page lists every collection with an estimated count` |
| Shell | Keyboard shortcuts and their help | `e2e:shortcuts.spec.ts#? opens the help, which lists the shortcuts, and Escape closes it` |
| Shell | Light and dark theme | `e2e:studio.spec.ts#the theme toggle switches and remembers the palette` |
| Shell | Live refresh of what is on screen (the old /events stream) | `e2e:live.spec.ts#Live is off by default; turning it on refetches the visible rows and keeps scroll and focus`; `go:TestRefreshInterval` |
| Shell | A source, table, collection or key that does not exist is a 404 | `go:TestAPISQLRead`; `go:TestAPIMongoRead`; `go:TestAPIRedisRead`; `go:TestAPIRedisMissingKeyIs404` |
| Shell | The root and the old paths | `go:TestRootRedirectsToUI`; `go:TestLegacyPagesAreGone`; `e2e:studio.spec.ts#the root and the old pages lead to the interface or to 404` |
| SQL read | Tables list with row estimates | `go:TestAPISQLRead`; `e2e:studio.spec.ts#opens a source and lists its tables with row estimates` |
| SQL read | Rows grid sorted by a header, by the server | `e2e:grid.spec.ts#sorting by a header is done by the server and shown with aria-sort` |
| SQL read | Filters with operators that follow the column type | `e2e:filters.spec.ts#adds a filter, which the server applies and the URL keeps`; `e2e:filters.spec.ts#NULL conditions need no value and the operators follow the column type` |
| SQL read | A malformed filter is refused, not hidden | `go:TestAPISQLRead`; `e2e:filters.spec.ts#a filter that does not parse is reported, not hidden` |
| SQL read | LIKE wildcards in a filter value are escaped | `go:TestSecurity_A03_StudioEscapesLikeWildcards` |
| SQL read | A table, column or sort name that the catalog does not list builds no SQL | `go:TestSecurity_A03_StudioRejectsUnknownIdentifiers`; `go:TestSecurityAPI` |
| SQL read | Keyset paging, next and previous | `go:TestRowsKeysetPaging`; `e2e:grid.spec.ts#Ctrl+End reaches the last row of a keyset view` |
| SQL read | Offset paging and its page cap, page-size limits | `go:TestBuildRows`; `go:TestGridSQL`; `e2e:perf.spec.ts#the end of a 100,000-row table is reachable and the scroll size stays below the browser limit` |
| SQL read | One row in full: JSON, NULL and binary values | `e2e:drawer.spec.ts#shows JSON formatted, with a raw view, and NULL and binary as tokens`; `go:TestCellsAreBounded` |
| SQL read | Foreign keys link to the referenced row, and the row lists who refers to it | `e2e:drawer.spec.ts#a foreign key links to the referenced row and the drawer lists what refers to a row`; `go:TestAPISQLRead` |
| SQL read | A table without a primary key is read-only | `e2e:nopk.spec.ts#a table without a primary key shows a notice and offers no edit in write mode`; `go:TestSecurityAPISQLWriteNeedsAPrimaryKey` |
| SQL read | Export as CSV or JSON Lines: capped, with the filter, safe from formulas | `go:TestAPIExportRows`; `go:TestExportFilenameAndCSVFormulas`; `go:TestSecurity_A03_StudioExportNeutralizesFormulas`; `e2e:live.spec.ts#the export link downloads the rows of the current filter and sort` |
| SQL read | A value that holds HTML is shown as text | `go:TestAPICellsHoldingHTMLAreJSONText`; `e2e:mongo-read.spec.ts#a document that holds markup is shown as text and runs nothing` |
| SQL write | Insert a row: defaults, NULL, typed values | `go:TestAPISQLWriteFlows`; `go:TestCoerce`; `e2e:edit.spec.ts#New row adds a row with defaults and shows it` |
| SQL write | Edit a row: only the columns sent change, a stale write is refused | `go:TestAPISQLWriteFlows`; `go:TestUpdateExpectGuard`; `e2e:edit.spec.ts#a double click edits a cell, Enter saves, and the value persists after a reload` |
| SQL write | Delete a row with its key typed, composite keys joined by commas | `go:TestSecurityAPISQLDeleteNeedsTheTypedKeyAndIsAudited`; `go:TestAPIWriteNeedsConfirm`; `e2e:edit.spec.ts#delete asks for the key to be typed and then removes the row` |
| SQL write | Unknown columns and bad values are refused and nothing is written | `go:TestSecurityAPISQLWriteRejectsUnknownColumnsAndBadValues`; `go:TestAPIUpdateRefusals`; `e2e:edit.spec.ts#an invalid value stays in the editor with a message; the server's refusal rolls the cell back` |
| SQL write | Without --write the write routes do not exist | `go:TestAPIWriteRoutesAbsentWithoutFlag`; `e2e:readonly.spec.ts#read-only: the write routes of the API answer 404 and change nothing` |
| SQL write | A write needs the session and the same origin | `go:TestSecurityAPI`; `go:TestSecurity_A01_StudioRejectsCrossOriginPost` |
| SQL write | Every write is audited without its values | `go:TestAPIWriteAudited`; `go:TestAPIWriteFailuresKeepValuesOutOfTheLog`; `go:TestWriteFailureLogsNoDatabaseText` |
| SQL console | Read-only: a write, a second statement or a setting is refused | `go:TestSecurity_A01_StudioConsoleIsReadOnly`; `go:TestReadOnlyStatement`; `go:TestSecurity_A01_StudioConsoleRefusesMultipleStatements`; `go:TestAPIQueryReadOnly`; `e2e:console-readonly.spec.ts#read-only Studio: no Write mode switch, and an INSERT is refused with the server's message` |
| SQL console | Write mode behind --write: atomic and audited | `go:TestAPIQueryWriteIsAtomicAndNeedsTheSwitch`; `go:TestAPIQueryWriteWithFlagIsAuditedWithoutTheStatement`; `e2e:console-write.spec.ts#Write mode is off when the page opens, and a write is refused until it is on` |
| SQL console | Rows affected, and the 1000-row result limit | `go:TestAPIQueryReportsRowsAffected`; `go:TestConsoleTruncatesAt1000Rows`; `e2e:console-run.spec.ts#a select of 3,000 rows shows only 1000 and says so` |
| SQL console | Explain shows the plan | `go:TestAPIExplain`; `e2e:console-run.spec.ts#explain shows the plan as text` |
| SQL console | Database errors never reach the client | `go:TestAPIQueryErrorsHideTheDatabase`; `go:TestConsoleLogsNeverHoldTheStatementOrAValue` |
| MongoDB | Collections page with estimated counts | `go:TestAPIMongoRead`; `go:TestMongoCollectionsAreSortedWithEstimates`; `e2e:mongo-read.spec.ts#the collections page lists every collection with an estimated count` |
| MongoDB | Documents filtered by field path, operator and scalar, built on the server | `go:TestMongoFilter`; `go:TestSecurity_A03_StudioMongoRejectsOperatorFields`; `go:TestSecurity_A03_StudioMongoRejectsObjectValues`; `e2e:mongo-read.spec.ts#a filter of field, operator and value is applied by the server and can be removed` |
| MongoDB | Sort by _id with a cursor, or by a field with the page cap | `go:TestMongoDocumentsKeysetPaging`; `go:TestMongoDocumentsOtherSortUsesSkip`; `e2e:mongo-read.spec.ts#sorting by a field explains the page limit and the server sorts`; `e2e:mongo-read.spec.ts#documents load as you scroll and only a window of rows is in the DOM` |
| MongoDB | One document in full | `e2e:mongo-read.spec.ts#a document opens in a drawer as a tree and as Extended JSON, and Escape closes it`; `go:TestAPIMongoRead` |
| MongoDB | Find and aggregate in the console | `e2e:mongo-console.spec.ts#a find runs and shows the documents in the result grid`; `e2e:mongo-console.spec.ts#an aggregate runs`; `go:TestMongoConsoleDecoding` |
| MongoDB | The console refuses server-side JavaScript, and $out and $merge without --write | `go:TestSecurityConsoleRefusalsThroughTheAPI`; `go:TestSecurity_A03_StudioMongoBlocksServerJS`; `go:TestSecurity_A01_StudioMongoBlocksWriteStagesReadOnly`; `e2e:mongo-console.spec.ts#operators that run JavaScript are refused, and so are $out and $merge without --write`; `e2e:mongo-console.spec.ts#the server refuses what the page did not catch (a script operator sent by hand)` |
| MongoDB | Insert a document | `go:TestAPIMongoWrite`; `go:TestMongoWriteBodiesAreChecked`; `e2e:mongo-write.spec.ts#insert adds a document, and invalid JSON cannot be sent` |
| MongoDB | Replace a document, its _id unchanged | `go:TestMongoReplaceChecksIDAndMatch`; `e2e:mongo-write.spec.ts#replace edits the whole document and needs the _id typed` |
| MongoDB | Delete a document with its _id typed | `go:TestAPIWriteNeedsConfirm`; `e2e:mongo-write.spec.ts#delete needs the _id typed and removes the document`; `e2e:mongo-write.spec.ts#the server refuses a delete without the typed _id even when asked directly` |
| MongoDB | An aggregate that writes is confirmed and audited | `e2e:mongo-write.spec.ts#an aggregate that writes needs Write mode and the source typed`; `go:TestAPIMongoWrite` |
| MongoDB | Export documents as JSON Lines | `go:TestAPIExportDocuments`; `e2e:mongo-read.spec.ts#the export link downloads the documents of the current filter as JSON Lines` |
| MongoDB | Without --write there is no write route | `go:TestAPIMongoWrite`; `e2e:mongo-read.spec.ts#read-only: no insert, replace or delete, and the write routes do not exist` |
| Redis | Keys listed with SCAN, a pattern, one step at a time | `go:TestSecurity_A04_StudioRedisNeverUsesKEYS`; `e2e:redis-read.spec.ts#Load more reads one SCAN step at a time until the cursor is 0`; `e2e:redis-read.spec.ts#the browser explains that SCAN is incremental` |
| Redis | A key that SCAN returns twice is listed once | `go:TestRedisKeysDedupeAndDescribe`; `e2e:redis-read.spec.ts#a key that SCAN returns twice is listed once`; `bun:src/grid/stream.test.ts#a key that SCAN returned twice is shown once, and counted` |
| Redis | Type and time to live in the list | `e2e:redis-read.spec.ts#rows show the type badge text and the time to live` |
| Redis | A key page for each type | `e2e:redis-types.spec.ts#a string shows its value, its size and its type`; `e2e:redis-types.spec.ts#a hash is a grid of fields and values`; `e2e:redis-types.spec.ts#a list has an index and a value`; `e2e:redis-types.spec.ts#a set lists its members`; `e2e:redis-types.spec.ts#a sorted set has a member and a score`; `e2e:redis-types.spec.ts#a stream has an id and its fields` |
| Redis | Binary values are visible and safe | `go:TestRedisDisplayEscapesBinary`; `go:TestRedisBinaryStringIsFlagged`; `e2e:redis-types.spec.ts#a binary string opens as a hex dump, with a text view`; `bun:src/redis/hex.test.ts#reads the server's escapes and the plain bytes between them` |
| Redis | Long keys are read a page at a time, with a count | `e2e:redis-types.spec.ts#a long hash pages with More and shows a visible count` |
| Redis | Edit a string keeping its expiry, set or clear the expiry | `go:TestAPIRedisSetKeepsTTLAndTTLAction`; `go:TestRedisWritesUseTheDocumentedCommands`; `e2e:redis-write.spec.ts#editing a string asks for a confirmation and keeps the expiry`; `e2e:redis-write.spec.ts#the expiry is set and removed` |
| Redis | Edit a hash field | `e2e:redis-write.spec.ts#a hash field is edited from its row` |
| Redis | Delete a key with its name typed | `go:TestAPIWriteNeedsConfirm`; `e2e:redis-write.spec.ts#delete needs the key typed`; `e2e:redis-write.spec.ts#the server refuses a delete whose confirmation is not the key` |
| Redis | The console accepts an allow-list and refuses the rest with the same message | `go:TestSecurity_A01_StudioRedisAllowsOnlyListedCommands`; `go:TestSecurity_A01_StudioRedisSubcommandsRestricted`; `go:TestRedisNeverAllowsKeys`; `go:TestRedisCommandNamesAreASCII`; `e2e:redis-console.spec.ts#a command outside the allow-list is refused with the same message as before` |
| Redis | A write command needs --write; the console audits only the name and the argument count | `go:TestSecurity_A01_StudioRedisWriteCommandsNeedWriteFlag`; `go:TestAPIRedisConsoleAuditsOnlyTheCommandName`; `e2e:redis-console.spec.ts#a write command is refused without --write, and the server refuses it too`; `e2e:redis-write.spec.ts#the console runs a write command only in Write mode, after a confirmation` |
| Redis | Without --write there is no write route | `e2e:redis-types.spec.ts#without --write there is no edit, no expiry change and no delete, and the routes do not exist`; `go:TestAPIWriteRoutesAbsentWithoutFlag` |
| Security | Loopback peer and Host, no proxy headers | `go:TestSecurity_A01_StudioRejectsNonLoopbackPeer`; `go:TestSecurity_A01_StudioRejectsProxyHeaders`; `go:TestSecurity_A01_StudioRejectsForeignHost`; `go:TestSecurityAPI` |
| Security | The one-time start token becomes a session cookie | `go:TestSecurity_A07_StudioRequiresSessionToken`; `go:TestSecurity_A07_StudioTokenBecomesCookie`; `go:TestSecurity_A07_StudioTokenIsSingleUse`; `go:TestSecurity_A07_StudioTokensAreRandom`; `e2e:studio.spec.ts#the start token works once` |
| Security | Strict CSP and security headers, no inline code | `go:TestSecurity_A05_StudioSendsStrictCSP`; `go:TestBuiltUIIsCSPClean`; `go:TestUIServed`; `e2e:grid.spec.ts#a large table renders a window of rows and stays inside the CSP`; `e2e:studio.spec.ts#no request leaves 127.0.0.1` |
| Security | Production is refused with exit code 2 | `gocmd:TestSecurity_A05_StudioRefusesProduction`; `gocmd:TestProductionRefusalStillExitsTwo`; `gocmd:TestSecurity_A05_StudioWriteInProductionNeedsAllowProduction` |
| Security | The printed start URL opens the new interface | `gocmd:TestStartURLPointsToUI`; `gocmd:TestRunServesAndStops` |
| Security | Credentials never printed or logged | `gocmd:TestSecurity_A09_StudioNeverPrintsCredentials`; `go:TestRedact` |
