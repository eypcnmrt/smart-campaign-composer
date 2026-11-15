// Command eval scores the real model against the golden set (eval/golden.json) with deterministic checks. It is run by
// `make eval`, never by `make test`, because it spends money.
//
// It refuses to run unless LLM_MODE=anthropic is given explicitly and a key is set (the key is read from the environment and
// is never printed). -mock runs the demo model instead, for a free dry run of the runner itself; it writes no results.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/db"
	"smartcampaign/internal/eval"
	"smartcampaign/internal/llm"
)

const (
	envMaxUSD     = "EVAL_MAX_USD"
	defaultMaxUSD = 1.00
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, osGetenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	golden := fs.String("golden", "eval/golden.json", "the golden set")
	only := fs.String("only", "", "comma-separated brief ids to run (default: all)")
	out := fs.String("out", "eval/results", `directory for the result files; "none" writes nothing`)
	model := fs.String("model", "", "model to evaluate (default: ANTHROPIC_MODEL, then "+llm.DefaultModel+")")
	mock := fs.Bool("mock", false, "use the demo model: a free dry run of the runner, results are not written")
	compare := fs.String("compare", "", "two result JSON files, comma-separated: write their comparison to -out and exit (no model call, no database)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *compare != "" {
		return runCompare(*compare, *out, stdout, stderr)
	}

	maxUSD := defaultMaxUSD
	if v := strings.TrimSpace(osGetenv(envMaxUSD)); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			fmt.Fprintf(stderr, "eval: %s must be a positive number of US dollars\n", envMaxUSD)
			return 2
		}
		maxUSD = f
	}

	// The real API is used only when asked for by name, and the key only ever goes to the client.
	getenv := func(k string) string {
		switch {
		case k == llm.EnvMode && *mock:
			return llm.ModeSettingMock
		case k == llm.EnvAPIKey && *mock:
			return ""
		case k == llm.EnvModel && *model != "":
			return *model
		}
		return osGetenv(k)
	}
	if !*mock && strings.ToLower(strings.TrimSpace(getenv(llm.EnvMode))) != llm.ModeSettingAnthropic {
		fmt.Fprintf(stderr, "eval: this run spends money; give %s=%s explicitly (or -mock for a free dry run)\n", llm.EnvMode, llm.ModeSettingAnthropic)
		return 2
	}
	sel, err := llm.FromEnv(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err) // ErrNoAPIKey and ErrInvalidMode never contain the key
		return 2
	}

	briefs, err := eval.Load(*golden)
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return 2
	}
	if *only != "" {
		briefs, err = pick(briefs, strings.Split(*only, ","))
		if err != nil {
			fmt.Fprintf(stderr, "eval: %v\n", err)
			return 2
		}
	}

	url := osGetenv("DATABASE_URL")
	if url == "" {
		fmt.Fprintln(stderr, "eval: DATABASE_URL is not set")
		return 2
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	pool, err := db.WaitReady(waitCtx, url, db.Options{})
	cancel()
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return 1
	}
	defer pool.Close()

	plan := eval.PlanFor(sel.Client.Model(), len(briefs))
	fmt.Fprintf(stdout, "eval: mode %s, model %s, %d briefs\n", sel.Client.Mode(), sel.Client.Model(), plan.Briefs)
	fmt.Fprintf(stdout, "eval: planned calls %d (one segment and one content request per brief), at most %d with every request repaired twice\n", plan.PlannedCalls, plan.MaxCalls)
	fmt.Fprintf(stdout, "eval: estimate $%.2f if every answer is right first time, $%.2f if every request needs two repairs (from the token counts of the smoke test)\n", plan.TypicalUSD, plan.UpperUSD)
	fmt.Fprintf(stdout, "eval: budget $%.2f (%s): no call is sent once the spent total reaches it; a call already running can end slightly above\n", maxUSD, envMaxUSD)

	runner := eval.NewRunner(sel.Client, campaign.NewStore(pool), eval.Config{Model: sel.Client.Model(), MaxUSD: maxUSD})
	res, err := runner.Run(ctx, briefs, stdout)
	fmt.Fprintln(stdout)
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, res.Markdown())

	if !*mock && *out != "none" {
		jsonPath, mdPath, err := res.WriteFiles(*out)
		if err != nil {
			fmt.Fprintf(stderr, "eval: writing the results: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "\nwritten: %s, %s\n", jsonPath, mdPath)
	}
	if res.BudgetStopped {
		fmt.Fprintf(stderr, "eval: stopped by the budget after $%.4f\n", res.SpentUSD)
		return 3
	}
	return 0
}

// runCompare writes the side-by-side comparison of two result files into out.
func runCompare(files, out string, stdout, stderr io.Writer) int {
	paths := strings.Split(files, ",")
	if len(paths) != 2 || out == "none" {
		fmt.Fprintln(stderr, "eval: -compare takes exactly two result files, a,b, and needs a real -out directory")
		return 2
	}
	var res [2]eval.Result
	for i, p := range paths {
		r, err := eval.LoadResult(strings.TrimSpace(p))
		if err != nil {
			fmt.Fprintf(stderr, "eval: %v\n", err)
			return 2
		}
		res[i] = r
	}
	path, err := eval.WriteCompare(out, res[0], res[1])
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "written: %s\n", path)
	return 0
}

// pick keeps the briefs with the given ids, in the order of the golden set; an unknown id is an error.
func pick(briefs []eval.Brief, ids []string) ([]eval.Brief, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[strings.TrimSpace(id)] = true
	}
	var out []eval.Brief
	for _, b := range briefs {
		if want[b.ID] {
			out = append(out, b)
			delete(want, b.ID)
		}
	}
	if len(want) > 0 {
		var missing []string
		for id := range want {
			missing = append(missing, id)
		}
		return nil, errors.New("unknown brief id(s): " + strings.Join(missing, ", "))
	}
	return out, nil
}
