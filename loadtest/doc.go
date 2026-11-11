// Package loadtest is a native HTTP load generator built on the standard library: the engine behind vukem bench.
// It is not a micro-benchmark harness (that is task bench and internal/bench).
//
// A run uses one of two models. The closed model (Config.Concurrency) keeps N workers in a loop: when the server
// slows down the workers send less, so the generator hides the very slowness it should show. The open model
// (Config.Rate) sends requests at a constant arrival rate whether or not earlier ones have finished, and measures
// each latency from the instant the request was due, not from when it was sent, so a late generator or a saturated
// server shows up in the numbers instead of being omitted (coordinated omission). An arrival that finds
// Config.MaxWorkers requests in flight is counted in Summary.DroppedIterations, not queued.
//
// A load generator aimed at the wrong place is a denial-of-service tool, so every request is checked, not only the
// first: only http and https, no credentials in the URL, and a loopback host (localhost, *.localhost, 127.0.0.0/8,
// ::1) unless Config.AllowRemote names the host. Redirects are never followed, and the size of a run is capped.
//
//	sum, err := loadtest.Run(ctx, loadtest.Config{Concurrency: 4, Requests: 200},
//		loadtest.Static(loadtest.Request{URL: "http://127.0.0.1:3000/"}))
//
// Metric names follow k6. http_req_duration differs from k6 on purpose: it includes the generator's lateness in the
// open model.
package loadtest
