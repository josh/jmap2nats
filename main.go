package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(versionString())
		return
	}

	configFlag := flag.String("config", "", "Path to JSON config file (overrides JMAP2NATS_CONFIG and ./jmap2nats.json)")
	printConfig := flag.Bool("print-config", false, "Print the merged effective config and exit")
	versionFlag := flag.Bool("version", false, "Print version and exit")
	verbose := flag.Bool("verbose", false, "Enable debug-level logging")
	flag.Parse()

	if *versionFlag {
		fmt.Println(versionString())
		return
	}

	path := resolveConfigPath(*configFlag)
	cfg, err := LoadConfig(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "jmap2nats:", err)
		os.Exit(1)
	}

	if *printConfig {
		out, _ := json.MarshalIndent(cfg, "", "  ")
		fmt.Println(string(out))
		return
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	log.Info("starting jmap2nats", "config", path, "level", level)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, cfg, log); err != nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

func run(ctx context.Context, cfg Config, log *slog.Logger) error {
	jc, err := NewJMAPClient(cfg, log)
	if err != nil {
		return err
	}
	nr, err := ConnectNATS(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer nr.Close()

	br := NewBridge(cfg, log, jc, nr)
	if err := br.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := info.Main.Version
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if v == "" || v == "(devel)" {
		if rev == "" {
			return "devel"
		}
		short := rev
		if len(short) > 12 {
			short = short[:12]
		}
		if dirty {
			short += "+dirty"
		}
		return "devel " + short
	}
	return v
}

func resolveConfigPath(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv("JMAP2NATS_CONFIG"); env != "" {
		return env
	}
	return "jmap2nats.json"
}
