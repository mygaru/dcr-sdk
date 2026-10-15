package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Outcome is the verdict of one check.
type Outcome string

const (
	// Pass: the cloud took the request and answered it. What it answered does
	// not matter here - a check proves the setup, not the request.
	Pass Outcome = "PASS"

	// Warn: the cloud answered, but in a way worth a look - the identity was
	// refused at the application level, or the handler failed. The transport
	// and its authentication worked.
	Warn Outcome = "WARN"

	// Fail: the request never got an answer from the cloud's handler - the
	// port, the TLS handshake, the client certificate, the token or the route.
	Fail Outcome = "FAIL"

	// Skip: the check was not run.
	Skip Outcome = "SKIP"
)

// Result is one check and what came of it.
type Result struct {
	Name    string        `json:"name"`
	Outcome Outcome       `json:"outcome"`
	Detail  string        `json:"detail"`
	Took    time.Duration `json:"took_ns"`
}

func printTable(w io.Writer, results []Result) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tRESULT\tTOOK\tDETAIL")

	for _, r := range results {
		took := "-"
		if r.Took > 0 {
			took = r.Took.Round(time.Millisecond).String()
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Name, r.Outcome, took, oneLine(r.Detail))
	}

	_ = tw.Flush()
}

func printJSON(w io.Writer, results []Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(results)
}

// exitCode is 1 when any check failed, and when strict also when any warned.
func exitCode(results []Result, strict bool) int {
	for _, r := range results {
		if r.Outcome == Fail || (strict && r.Outcome == Warn) {
			return 1
		}
	}

	return 0
}

// oneLine keeps a table row on one line: server error bodies carry newlines
// and can be long.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")

	const limit = 160
	if len(s) > limit {
		s = s[:limit] + "..."
	}

	return s
}
