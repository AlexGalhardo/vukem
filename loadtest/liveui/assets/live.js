// The live dashboard: it opens the event stream and draws what it receives. Everything that reaches the page is set
// with textContent or SVG attributes: scenario names are user input, so there is no HTML string anywhere in here.
(function () {
	"use strict";

	var SVG = "http://www.w3.org/2000/svg";
	var MAX_POINTS = 3600;
	var W = 600;
	var H = 160;
	var PAD = 28;

	var snapshots = [];
	var title = "";
	var model = "closed";
	var thresholdNames = [];

	function el(id) {
		return document.getElementById(id);
	}

	function text(id, value) {
		el(id).textContent = value;
	}

	function svg(name, attrs) {
		var node = document.createElementNS(SVG, name);
		Object.keys(attrs || {}).forEach(function (key) {
			node.setAttribute(key, String(attrs[key]));
		});
		return node;
	}

	function clear(node) {
		while (node.firstChild) {
			node.removeChild(node.firstChild);
		}
	}

	function ms(micros) {
		var v = micros / 1000;
		return (v >= 100 ? v.toFixed(0) : v.toFixed(1)) + " ms";
	}

	function nanos(n) {
		return ms(n / 1000);
	}

	function clock(seconds) {
		var s = Math.max(0, Math.floor(seconds));
		return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
	}

	// A line chart: series is a list of {cls, values}; all share one scale so they can be compared.
	function line(groupId, series, fmt) {
		var group = el(groupId);
		clear(group);
		group.appendChild(svg("rect", { x: PAD, y: 4, width: W - PAD - 4, height: H - 8 - 14, "class": "frame" }));

		var top = 0;
		series.forEach(function (s) {
			s.values.forEach(function (v) {
				top = Math.max(top, v);
			});
		});
		if (snapshots.length === 0) {
			return;
		}

		var count = Math.max(snapshots.length - 1, 1);
		var plotW = W - PAD - 4;
		var plotH = H - 8 - 14;

		series.forEach(function (s) {
			var points = s.values.map(function (v, i) {
				var x = PAD + (i / count) * plotW;
				var y = 4 + plotH - (top > 0 ? (v / top) * plotH : 0);
				return x.toFixed(1) + "," + y.toFixed(1);
			});
			group.appendChild(svg("polyline", { "class": "line " + s.cls, points: points.join(" ") }));
		});

		var topLabel = svg("text", { x: 2, y: 12, "class": "axis" });
		topLabel.textContent = fmt(top);
		group.appendChild(topLabel);

		var zero = svg("text", { x: 2, y: H - 14, "class": "axis" });
		zero.textContent = fmt(0);
		group.appendChild(zero);

		var end = svg("text", { x: W - 4, y: H - 2, "class": "axis", "text-anchor": "end" });
		end.textContent = clock(snapshots[snapshots.length - 1].second) + " elapsed";
		group.appendChild(end);
	}

	function plain(v) {
		return String(Math.round(v));
	}

	function redraw() {
		line("chart-throughput", [{ cls: "reqs", values: snapshots.map(function (s) { return s.reqs; }) }], plain);
		line("chart-latency", [
			{ cls: "p50", values: snapshots.map(function (s) { return s.p50_us; }) },
			{ cls: "p95", values: snapshots.map(function (s) { return s.p95_us; }) },
			{ cls: "p99", values: snapshots.map(function (s) { return s.p99_us; }) }
		], ms);
		line("chart-failures", [{ cls: "failed", values: snapshots.map(function (s) { return s.failed; }) }], plain);
		line("chart-load", [
			{ cls: "active", values: snapshots.map(function (s) { return s.active; }) },
			{ cls: "target", values: snapshots.map(function (s) { return s.target_rate; }) }
		], plain);

		var last = snapshots[snapshots.length - 1];
		if (!last) {
			return;
		}
		text("card-rps", String(last.reqs));
		text("card-p95", ms(last.p95_us));
		var failedPct = last.total_reqs > 0 ? (100 * last.total_failed) / last.total_reqs : 0;
		text("card-failed", failedPct.toFixed(2) + " %");
		text("card-target", String(last.target_rate));
		text("card-target-label", model === "open" ? "target arrivals per second" : "virtual users");
		text("elapsed", clock(last.second + 1));
	}

	function cell(row, value) {
		var td = document.createElement("td");
		td.textContent = value;
		row.appendChild(td);
	}

	function drawNames(sum) {
		var body = el("names");
		clear(body);

		var names = sum.by_name || {};
		var keys = Object.keys(names).sort();
		if (keys.length === 0 && sum.http_reqs > 0) {
			names = { "all requests": { http_reqs: sum.http_reqs, http_req_failed: sum.http_req_failed, http_req_duration: sum.http_req_duration } };
			keys = ["all requests"];
		}

		keys.forEach(function (name) {
			var n = names[name];
			var row = document.createElement("tr");
			cell(row, name);
			cell(row, String(n.http_reqs));
			cell(row, (100 * n.http_req_failed).toFixed(2) + " %");
			cell(row, nanos(n.http_req_duration.p50_ns));
			cell(row, nanos(n.http_req_duration.p95_ns));
			cell(row, nanos(n.http_req_duration.p99_ns));
			body.appendChild(row);
		});
	}

	function drawHistogram(sum) {
		var group = el("chart-histogram");
		clear(group);

		var rows = sum.histogram || [];
		var peak = 0;
		rows.forEach(function (r) {
			peak = Math.max(peak, r.count);
		});
		if (peak === 0) {
			return;
		}

		var slot = (W - 8) / rows.length;
		rows.forEach(function (r, i) {
			var h = (r.count / peak) * (H - 60);
			group.appendChild(svg("rect", { x: 4 + i * slot + 2, y: 90 - h, width: Math.max(slot - 4, 1), height: h, "class": "bar" }));

			var label = svg("text", { x: 4 + i * slot + slot / 2, y: 106, "class": "axis", "text-anchor": "middle" });
			label.textContent = nanos(r.to_ns);
			group.appendChild(label);
		});
	}

	function drawThresholds(results) {
		var list = el("thresholds");
		clear(list);

		if (!results || results.length === 0) {
			var none = document.createElement("li");
			none.className = "empty";
			none.textContent = "none set";
			list.appendChild(none);
			return;
		}

		results.forEach(function (r) {
			var item = document.createElement("li");
			item.className = r.ok ? "ok" : "fail";
			item.textContent = r.threshold + " (got " + r.got + ")";
			list.appendChild(item);
		});
	}

	function summary(data) {
		var sum = data.summary || data;
		drawNames(sum);
		drawHistogram(sum);
		if (data.thresholds) {
			drawThresholds(data.thresholds);
		}
	}

	function setStatus(value) {
		var badge = el("status");
		badge.textContent = value;
		badge.className = "badge " + value;
	}

	var source = new EventSource("/events");

	source.addEventListener("history", function (event) {
		var data = JSON.parse(event.data);
		snapshots = data.snapshots || [];
		title = data.title || "";
		model = data.model || "closed";
		thresholdNames = data.thresholds || [];
		text("title", title);
		setStatus("running");
		redraw();
	});

	source.addEventListener("snapshot", function (event) {
		snapshots.push(JSON.parse(event.data));
		if (snapshots.length > MAX_POINTS) {
			snapshots.shift();
		}
		redraw();
	});

	source.addEventListener("summary", function (event) {
		summary(JSON.parse(event.data));
	});

	source.addEventListener("finished", function (event) {
		summary(JSON.parse(event.data));
		setStatus("finished");
		source.close();
	});
})();
