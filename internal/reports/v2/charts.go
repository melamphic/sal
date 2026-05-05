package v2

// chartJSCDN returns the <head> snippet that loads Chart.js. Reports
// that render charts include this in pdf.ReportInput.ExtraHead. The
// Renderer also sets WaitForExpression so Gotenberg holds the snapshot
// until the per-report init script flips window.__chartsReady = true.
//
// Pinned to a specific version so Chromium-side cache + reproducibility
// stay deterministic.
func chartJSCDN() string {
	return `<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.0/dist/chart.umd.min.js"></script>`
}
