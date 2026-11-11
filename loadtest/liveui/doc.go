// Package liveui is the read-only live dashboard of a load test (vukem bench --ui) and its self-contained HTML
// report (vukem bench --report).
//
// The dashboard only watches: it has no control that changes the load, and every route is a GET. It listens on
// 127.0.0.1 only, asks for the session token printed at start (the start URL carries it once and trades it for an
// HttpOnly cookie), refuses any other Host or peer and any proxy header, and sends a strict Content-Security-Policy:
// scripts and styles come from embedded files, nothing is inline and nothing comes from another origin. The page
// draws its charts as SVG with a small script that only ever sets textContent, because scenario names are user input.
//
// A Server keeps one snapshot per second of the run (at most an hour) for the charts and the report. Feed it the
// Results of a loadtest.Runner, or call Observe and Tick yourself with a fake clock in a test.
package liveui
