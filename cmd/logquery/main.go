package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/polarfoxDev/marina/internal/logging"
)

func main() {
	dbPath := flag.String("db", "/var/lib/marina/logs.db", "Path to logs database")
	jobID := flag.String("job", "", "Filter by job ID")
	instanceID := flag.String("instance", "", "Filter by instance ID")
	level := flag.String("level", "", "Filter by log level (DEBUG, INFO, WARN, ERROR)")
	since := flag.String("since", "", "Filter logs since time (RFC3339 format)")
	until := flag.String("until", "", "Filter logs until time (RFC3339 format)")
	limit := flag.Int("limit", 100, "Maximum number of logs to return")
	prune := flag.String("prune", "", "Prune logs older than duration (e.g., '720h' for 30 days)")
	
	flag.Parse()

	logger, err := logging.New(*dbPath, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	// Handle pruning if requested
	if *prune != "" {
		duration, err := time.ParseDuration(*prune)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid duration format: %v\n", err)
			os.Exit(1)
		}
		deleted, err := logger.PruneOldLogs(duration)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error pruning logs: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Pruned %d log entries older than %v\n", deleted, duration)
		return
	}

	// Build query options
	opts := logging.QueryOptions{
		JobID:      *jobID,
		InstanceID: *instanceID,
		Limit:      *limit,
	}

	if *level != "" {
		opts.Level = logging.LogLevel(*level)
	}

	if *since != "" {
		t, err := time.Parse(time.RFC3339, *since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid since time format: %v\n", err)
			os.Exit(1)
		}
		opts.Since = t
	}

	if *until != "" {
		t, err := time.Parse(time.RFC3339, *until)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid until time format: %v\n", err)
			os.Exit(1)
		}
		opts.Until = t
	}

	// Query logs
	entries, err := logger.Query(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error querying logs: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Println("No logs found matching criteria")
		return
	}

	// Print results in a table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIMESTAMP\tLEVEL\tJOB\tINSTANCE\tMESSAGE")
	fmt.Fprintln(w, "─────────\t─────\t───\t────────\t───────")
	
	for _, entry := range entries {
		ts := entry.Timestamp.Format("2006-01-02 15:04:05")
		job := entry.JobID
		if job == "" {
			job = "-"
		}
		instance := entry.InstanceID
		if instance == "" {
			instance = "-"
		}
		// Truncate message if too long
		msg := entry.Message
		if len(msg) > 80 {
			msg = msg[:77] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", ts, entry.Level, job, instance, msg)
	}
	
	w.Flush()
	fmt.Printf("\nShowing %d results\n", len(entries))
}
