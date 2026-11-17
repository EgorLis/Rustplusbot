package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/EgorLis/Rustplusbot/internal/bmapi"
	"github.com/EgorLis/Rustplusbot/internal/bot"
	"github.com/EgorLis/Rustplusbot/internal/rpclient"
	"golang.org/x/sys/windows"
)

func readConfig(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	if json.Unmarshal(data, out) != nil {
		return fmt.Errorf("invalid JSON in %s", path)
	}

	return nil
}

func main() {
	code := runMain()
	if code != 0 {
		var mode uint32
		if windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil {
			fmt.Fprintln(os.Stderr, "\u041e\u0448\u0438\u0431\u043a\u0430 \u0437\u0430\u043f\u0443\u0441\u043a\u0430. \u041f\u043e\u0434\u0440\u043e\u0431\u043d\u043e\u0441\u0442\u0438: logs/bot.log.")
			fmt.Fprintln(os.Stderr, "\u041d\u0430\u0436\u043c\u0438\u0442\u0435 Enter \u0434\u043b\u044f \u0432\u044b\u0445\u043e\u0434\u0430...")
			bufio.NewReader(os.Stdin).ReadString('\n')
		}
	}

	os.Exit(code)
}

func runMain() int {
	if err := run(); err != nil {
		if errors.Is(err, context.Canceled) {
			return 0
		}

		log.Printf("ERROR: %v", err)
		return 1
	}

	return 0
}

func run() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	defaultDir := filepath.Join(filepath.Dir(exe), "conf")
	flags := flag.NewFlagSet("rustplusbot", flag.ContinueOnError)
	configDir := flags.String("config-dir", defaultDir, "configuration directory")
	check := flags.Bool("check-connection", false, "verify Rust+ connection without hooks, chat messages or device actions")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	conf, err := filepath.Abs(*configDir)
	if err != nil {
		return err
	}

	if err := os.Chdir(filepath.Dir(exe)); err != nil {
		return err
	}

	if err := os.MkdirAll("logs", 0755); err != nil {
		return err
	}

	logFile, err := os.OpenFile("logs/bot.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	previous := log.Writer()
	log.SetOutput(io.MultiWriter(os.Stderr, logFile))

	defer func() {
		log.SetOutput(previous)
		logFile.Close()
	}()

	// Log errors before closing the file, including startup and config errors.
	return runBot(conf, *check)
}

func runBot(conf string, check bool) (result error) {
	defer func() {
		if result != nil && !errors.Is(result, context.Canceled) {
			log.Printf("Bot stopped: %v", result)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var rpcfg rpclient.RustPlusConfig
	if err := readConfig(filepath.Join(conf, "rpconfig.json"), &rpcfg); err != nil {
		return err
	}

	if rpcfg.AuthTokenFile != "" && !filepath.IsAbs(rpcfg.AuthTokenFile) {
		rpcfg.AuthTokenFile = filepath.Join(conf, rpcfg.AuthTokenFile)
	}

	b := bot.New()
	if err := b.SetRustPlusClient(rpcfg); err != nil {
		return err
	}

	if !check {
		var bmcfg bmapi.BMConf
		if err := readConfig(filepath.Join(conf, "bmconfig.json"), &bmcfg); err != nil {
			return err
		}

		b.SetBattleMetrics(bmcfg)
		if err := b.SetMediaHook(); err != nil {
			return fmt.Errorf("initialize media hook: %w", err)
		}

		sound := "3.mp3"
		b.SetCheckPlayerDeath(rpcfg.PlayerID, &sound)
		if err := b.UseConfig(filepath.Join(conf, "botconfig.json")); err != nil {
			return err
		}
	}

	if err := b.StartContext(ctx); err != nil {
		return err
	}

	defer b.Stop()

	if check {
		log.Println("Rust+ connection check passed (GetInfo verified); no device or chat actions sent")
		return nil
	}

	log.Println("Bot is running. Press Ctrl+C to stop.")
	<-ctx.Done()
	return nil
}
