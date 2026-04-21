package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/czNail/pg_cpu_profile/internal/pgcpu"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx := context.Background()

	switch os.Args[1] {
	case "run":
		if err := runCommand(ctx, os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "pgcpu run: %v\n", err)
			os.Exit(1)
		}
	case "attach":
		if err := attachCommand(ctx, os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "pgcpu attach: %v\n", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n")
	fmt.Fprintf(os.Stderr, "  pgcpu run --dsn <dsn> --sql <sql> [--json <path>]\n")
	fmt.Fprintf(os.Stderr, "  pgcpu attach --dsn <dsn> --pid <pid> [--json <path>]\n")
}

func runCommand(parent context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var opts pgcpu.RunOptions

	fs.StringVar(&opts.DSN, "dsn", "", "PostgreSQL DSN")
	fs.StringVar(&opts.SQL, "sql", "", "SQL statement to execute")
	fs.StringVar(&opts.JSONPath, "json", "", "Write JSON report to this path")
	fs.DurationVar(&opts.PollInterval, "poll-interval", 100*time.Millisecond, "Observer poll interval")
	fs.DurationVar(&opts.ResultTimeout, "result-timeout", 10*time.Second, "Wait timeout for last-query capture")
	fs.BoolVar(&opts.DisableParallel, "disable-parallel", true, "Disable parallel query in the target session")
	fs.BoolVar(&opts.DisableJIT, "disable-jit", true, "Disable JIT in the target session")
	fs.StringVar(&opts.GoBinary, "go-binary", "", "unused placeholder for future compatibility")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.DSN == "" {
		return fmt.Errorf("--dsn is required")
	}
	if opts.SQL == "" {
		return fmt.Errorf("--sql is required")
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	result, err := pgcpu.Run(ctx, opts)
	if err != nil {
		return err
	}

	fmt.Print(pgcpu.FormatText(result))
	if err := pgcpu.WriteJSON(result, opts.JSONPath); err != nil {
		return err
	}
	return nil
}

func attachCommand(parent context.Context, args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var opts pgcpu.AttachOptions

	fs.StringVar(&opts.DSN, "dsn", "", "PostgreSQL DSN")
	fs.IntVar(&opts.PID, "pid", 0, "Target backend PID")
	fs.StringVar(&opts.JSONPath, "json", "", "Write JSON report to this path")
	fs.DurationVar(&opts.PollInterval, "poll-interval", 100*time.Millisecond, "Observer poll interval")
	fs.DurationVar(&opts.ResultTimeout, "result-timeout", 30*time.Second, "Wait timeout for last-query capture")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if opts.DSN == "" {
		return fmt.Errorf("--dsn is required")
	}
	if opts.PID <= 0 {
		return fmt.Errorf("--pid must be greater than zero")
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	result, err := pgcpu.Attach(ctx, opts)
	if err != nil {
		return err
	}

	fmt.Print(pgcpu.FormatText(result))
	if err := pgcpu.WriteJSON(result, opts.JSONPath); err != nil {
		return err
	}
	return nil
}
