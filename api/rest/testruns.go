package rest

import (
	"fmt"
	"html"
	"net/http"
	"slices"
	"time"

	"runtime.link/api"
	"runtime.link/api/test"
)

// yieldTestRuns mounts the builtin /testruns endpoints for a test
// implementation that carries an assigned [test.History]:
//
//	GET  /testruns          overview of the latest pass/fail status per test
//	GET  /testruns/{name}    show the recorded history for a test (does not run it)
//	POST /testruns/{name}    run the named test, record it, and redirect back
//
// Viewing a test never executes it; execution only happens on the POST, which
// is triggered by the "Run test" button on the detail page. This keeps side
// effects (real API calls against a live environment) deliberate.
//
// It returns false if the yield function requested that route iteration stop.
func yieldTestRuns(auth api.Auth[*http.Request], yield func(string, http.Handler) bool, tested api.WithTests) bool {
	if !yield("GET /testruns", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addCORS(auth, w, r, api.Function{})
		history := tested.History(r.Context())
		if history == nil {
			http.NotFound(w, r)
			return
		}
		summaries, err := history.Summary(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tests, _ := tested.Tests(r.Context())
		// Backfill the source file for each summary from the enumerated
		// tests (grouped by file), so the overview can group by file even
		// when the History implementation does not persist it.
		fillSummaryFrom(summaries, tests)
		writeTestRunHead(w, false)
		writeTestRunNav(w, tests, "", "")
		w.Write([]byte("<main>"))
		defer w.Write([]byte("</main></body></html>"))
		fmt.Fprintf(w, "<h1>Test Runs</h1>")
		writeTestRunOverview(w, summaries)
	})) {
		return false
	}
	if !yield("GET /testruns/{name}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addCORS(auth, w, r, api.Function{})
		name := r.PathValue("name")
		history := tested.History(r.Context())
		if history == nil {
			http.NotFound(w, r)
			return
		}
		// Confirm the test exists without running it.
		tests, _ := tested.Tests(r.Context())
		if !testKnown(tests, name) {
			http.NotFound(w, r)
			return
		}
		writeTestRunHead(w, true)
		// The detail page lives one path segment deeper than the
		// overview, so navigation links are prefixed with "../".
		writeTestRunNav(w, tests, name, "../")
		w.Write([]byte("<main>"))
		defer w.Write([]byte("</main></body></html>"))
		fmt.Fprintf(w, "<h1>%s</h1>", html.EscapeString(formatExampleCategory(name)))
		writeTestRunButton(w, name)

		runs, err := history.Inspect(r.Context(), name)
		fmt.Fprintf(w, "<h2>History</h2>")
		var count int
		if err == nil && runs != nil {
			for past := range runs {
				writeTestRunHistoryEntry(w, past, count == 0)
				count++
			}
		}
		if count == 0 {
			fmt.Fprintf(w, "<p>No runs recorded yet. Run the test to record one.</p>")
		}
	})) {
		return false
	}
	if !yield("POST /testruns/{name}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addCORS(auth, w, r, api.Function{})
		name := r.PathValue("name")
		history := tested.History(r.Context())
		if history == nil {
			http.NotFound(w, r)
			return
		}
		exec, ok := tested.Test(r.Context(), name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		// Persist this execution so it appears in the test's history.
		// A capture failure is non-fatal.
		_ = history.Capture(r.Context(), exec)
		// Redirect back to the detail page so a refresh doesn't re-run
		// the test (POST/redirect/GET).
		http.Redirect(w, r, "./"+name, http.StatusSeeOther)
	})) {
		return false
	}
	return true
}

// testKnown reports whether name appears in the enumerated test categories.
func testKnown(tests map[string][]string, name string) bool {
	for _, names := range tests {
		if slices.Contains(names, name) {
			return true
		}
	}
	return false
}

// formatDuration renders a [time.Duration] for display in the test run UI.
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return d.String()
}

// testRunStatus returns a badge glyph and label for an execution outcome.
func testRunStatus(errText string, panicked bool) (glyph, label string) {
	switch {
	case panicked:
		return "💥", "panic"
	case errText != "":
		return "❌", "fail"
	default:
		return "✅", "pass"
	}
}

// writeTestRunHead writes the shared HTML head + opening body tag for the
// test run pages, reusing the documentation stylesheet. nested selects the
// head variant whose asset URLs resolve from one path segment deeper.
func writeTestRunHead(w http.ResponseWriter, nested bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte("<!DOCTYPE html>"))
	if nested {
		w.Write(docs_head_nested)
	} else {
		w.Write(docs_head)
	}
	w.Write([]byte("<body>"))
}

// writeTestRunNav renders the left-hand navigation listing every test,
// grouped by category, highlighting the current selection. prefix is
// prepended to links so pages at different path depths resolve correctly.
func writeTestRunNav(w http.ResponseWriter, tests map[string][]string, current, prefix string) {
	if len(tests) == 0 {
		return
	}
	w.Write([]byte("<nav>"))
	fmt.Fprintf(w, "<h2><a href=\"%sdocumentation\">← API Reference</a></h2>", prefix)
	fmt.Fprintf(w, "<h3><a href=\"%stestruns\">Test Runs</a></h3>", prefix)
	w.Write([]byte("<div class=\"examples-list\">"))
	categories := slices.Sorted(func(yield func(string) bool) {
		for k := range tests {
			if !yield(k) {
				return
			}
		}
	})
	for _, category := range categories {
		names := tests[category]
		if slices.Contains(names, current) {
			fmt.Fprintf(w, "<details class=\"example-category\" open>")
		} else {
			fmt.Fprintf(w, "<details class=\"example-category\">")
		}
		fmt.Fprintf(w, "<summary class=\"category-header\">%s</summary>", formatExampleCategory(category))
		fmt.Fprintf(w, "<div class=\"category-examples\">")
		for _, name := range names {
			title := formatExampleCategory(name)
			class := "example-link"
			if name == current {
				class = "example-link current-example"
			}
			fmt.Fprintf(w, "<a href=\"%stestruns/%s\" class=\"%s\">%s</a>", prefix, name, class, title)
		}
		fmt.Fprintf(w, "</div></details>")
	}
	w.Write([]byte("</div></nav>"))
}

// fillSummaryFrom populates the From (source file) of each summary that does
// not already carry one, looking the test name up in the file-grouped tests
// map. History implementations that persist From take precedence.
func fillSummaryFrom(summaries []test.Summary, tests map[string][]string) {
	if len(tests) == 0 {
		return
	}
	fileOf := make(map[string]string)
	for file, names := range tests {
		for _, name := range names {
			fileOf[name] = file
		}
	}
	for i := range summaries {
		if summaries[i].From == "" {
			summaries[i].From = fileOf[summaries[i].Name]
		}
	}
}

// writeTestRunOverview renders the pass/fail grid for all tests.
func writeTestRunOverview(w http.ResponseWriter, summaries []test.Summary) {
	if len(summaries) == 0 {
		fmt.Fprintf(w, "<p>No tests have been run yet. Select a test to run it.</p>")
		return
	}
	var passed, failed int
	for _, s := range summaries {
		if s.Pass {
			passed++
		}
		if s.Fail {
			failed++
		}
	}
	fmt.Fprintf(w, "<div class=\"markdown\">%d passing, %d failing, %d total.</div>", passed, failed, len(summaries))

	// Group the tests by the file they were declared in (Summary.From) so the
	// overview mirrors the source layout. Files are listed alphabetically and
	// tests keep their given order within each file.
	byFile := make(map[string][]test.Summary)
	for _, s := range summaries {
		from := s.From
		if from == "" {
			from = "uncategorized"
		}
		byFile[from] = append(byFile[from], s)
	}
	files := slices.Sorted(func(yield func(string) bool) {
		for k := range byFile {
			if !yield(k) {
				return
			}
		}
	})
	for _, file := range files {
		fmt.Fprintf(w, "<h2>%s</h2>", html.EscapeString(formatExampleCategory(file)))
		fmt.Fprintf(w, "<div class=\"examples-list\">")
		for _, s := range byFile[file] {
			var glyph string
			switch {
			case s.Fail:
				glyph = "❌"
			case s.Pass:
				glyph = "✅"
			default:
				glyph = "⚪"
			}
			fmt.Fprintf(w, "<div class=sample><pre>%s <a href=\"testruns/%s\">%s</a></pre></div>",
				glyph, html.EscapeString(s.Name), html.EscapeString(formatExampleCategory(s.Name)))
		}
		fmt.Fprintf(w, "</div>")
	}
}

// writeTestRunButton renders the form whose submission runs the test. It
// POSTs to the current detail URL, which executes the test, records it, and
// redirects back.
func writeTestRunButton(w http.ResponseWriter, name string) {
	fmt.Fprintf(w, `<form method="POST" action="%s"><button type="submit" class="run-test-button">▶ Run test</button></form>`,
		html.EscapeString(name))
}

// writeTestRunTrace renders the ordered trace of calls captured for an
// execution, showing arguments and return values where present.
func writeTestRunTrace(w http.ResponseWriter, trace []test.Event) {
	for _, event := range trace {
		if event.Note != "" {
			fmt.Fprintf(w, "<div class=\"markdown\">%s</div>", html.EscapeString(event.Note))
		}
		if event.Call == "" {
			continue
		}
		fmt.Fprintf(w, "<div class=sample><pre>%s</pre>", html.EscapeString(event.Call))
		if event.Docs != "" {
			fmt.Fprintf(w, "<div class=\"markdown call-docs\">%s</div>", html.EscapeString(event.Docs))
		}
		if len(event.Args) > 0 {
			fmt.Fprintf(w, "<b>Args:</b><pre>%s</pre>", html.EscapeString(string(event.Args)))
		}
		if len(event.Vals) > 0 {
			fmt.Fprintf(w, "<b>Returns:</b><pre>%s</pre>", html.EscapeString(string(event.Vals)))
		}
		fmt.Fprintf(w, "</div>")
	}
}

// writeTestRunHistoryEntry renders one recorded execution in a collapsible
// block. open controls whether the block is expanded by default; the caller
// expands the most recent run.
func writeTestRunHistoryEntry(w http.ResponseWriter, exec test.Execution, open bool) {
	glyph, label := testRunStatus(exec.Error, exec.Panic)
	if open {
		fmt.Fprintf(w, "<details open>")
	} else {
		fmt.Fprintf(w, "<details>")
	}
	fmt.Fprintf(w, "<summary>%s %s — %s</summary>", glyph, label, formatDuration(exec.Speed))
	if exec.Story != "" {
		fmt.Fprintf(w, "<div class=\"markdown\">%s</div>", html.EscapeString(exec.Story))
	}
	writeTestRunTrace(w, exec.Trace)
	if exec.Error != "" {
		fmt.Fprintf(w, "<b>Error:</b><pre>%s</pre>", html.EscapeString(exec.Error))
	}
	fmt.Fprintf(w, "</details>")
}
