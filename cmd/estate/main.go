// Command estate runs the Estate party board game: the PC table screen, the
// phone app, the classic client and the game API, all from one binary.
//
//	estate                      serve on :3210 and open the table screen
//	estate serve [flags]        serve (flags: --addr --public-url --data --no-open)
//	estate version              print the version
//
// Flags default from ESTATE_ADDR, ESTATE_PUBLIC_URL and ESTATE_DATA.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	estate "github.com/tdar200/estate-games"
	"github.com/tdar200/estate-games/internal/closest"
	"github.com/tdar200/estate-games/internal/poker/bots"
	"github.com/tdar200/estate-games/internal/quiz"
	"github.com/tdar200/estate-games/internal/server"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	args := os.Args[1:]
	command := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	switch command {
	case "serve":
		if err := serve(args); err != nil {
			fmt.Fprintln(os.Stderr, "estate:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println(version)
	case "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "estate: unknown command %q\n\n", command)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `Usage:
  estate                 serve on :3210 and open the table screen fullscreen
  estate serve [flags]   serve the game
  estate version         print the version

Flags for serve:
  --addr ADDR            listen address (default :3210, env ESTATE_ADDR)
  --public-url URL       public base URL for invite links and QR codes,
                         e.g. https://host.tailnet.ts.net:8443 (env ESTATE_PUBLIC_URL)
  --data DIR             directory for devices and friends (env ESTATE_DATA)
  --no-open              do not open the table screen in a browser
  --poker-bot SPEC       optional stronger poker AI for "hard" seats:
                         cmd:/path/to/bridge [args] or http://127.0.0.1:7420/decide
                         (env ESTATE_POKER_BOT; without it poker offers Easy and Normal)
`)
}

// pokerBotFrom starts the external poker bot named by spec ("cmd:prog args"
// or an http(s) URL). A bot that can't start is logged and left out: poker
// then offers only its built-in levels.
func pokerBotFrom(spec string, logger *log.Logger) (bots.Bot, func()) {
	spec = strings.TrimSpace(spec)
	switch {
	case spec == "":
		return nil, func() {}
	case strings.HasPrefix(spec, "cmd:"):
		parts := strings.Fields(strings.TrimPrefix(spec, "cmd:"))
		if len(parts) == 0 {
			logger.Printf("--poker-bot %q: no program given; poker uses its built-in bots", spec)
			return nil, func() {}
		}
		p, err := bots.StartProcess(parts[0], parts[1:]...)
		if err != nil {
			logger.Printf("--poker-bot: %v; poker uses its built-in bots", err)
			return nil, func() {}
		}
		logger.Printf("poker: hard seats play %s", parts[0])
		return p, func() { p.Close() }
	case strings.HasPrefix(spec, "http://"), strings.HasPrefix(spec, "https://"):
		logger.Printf("poker: hard seats ask %s", spec)
		return &bots.HTTPBot{URL: spec}, func() {}
	}
	logger.Printf("--poker-bot %q: expected cmd:<program> or an http URL; poker uses its built-in bots", spec)
	return nil, func() {}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := flags.String("addr", envOr("ESTATE_ADDR", ":3210"), "listen address")
	publicURL := flags.String("public-url", os.Getenv("ESTATE_PUBLIC_URL"), "public base URL")
	dataDir := flags.String("data", envOr("ESTATE_DATA", defaultDataDir()), "data directory")
	noOpen := flags.Bool("no-open", false, "do not open a browser")
	pokerBot := flags.String("poker-bot", os.Getenv("ESTATE_POKER_BOT"), "external poker bot")
	flags.Usage = func() { usage(os.Stderr) }
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	logger := log.New(os.Stderr, "estate: ", log.LstdFlags)

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	lanURL := fmt.Sprintf("http://localhost:%d", port)
	if ip := lanIPv4(); ip != "" {
		lanURL = fmt.Sprintf("http://%s:%d", ip, port)
	}
	// Poker is the second game type, Ludo the third, the quiz the fourth and Near Thing the
	// fifth. Poker's computer seats
	// are Easy and Normal; an external bot (the trained AI) adds "hard" when configured.
	external, closeExternal := pokerBotFrom(*pokerBot, logger)
	defer closeExternal()
	var pokerLevels []string
	if external != nil {
		pokerLevels = []string{"easy", "normal", "hard"}
	}
	if _, err := quiz.DefaultLibrary(); err != nil {
		// Not fatal: the other games still work, and a quiz says why it cannot start.
		logger.Printf("quiz packs are broken; quiz games cannot start: %v", err)
	}
	if lib, err := closest.DefaultLibrary(); err != nil {
		// Not fatal either: a closest game says why it cannot start.
		logger.Printf("%s packs are broken; %s games cannot start: %v", server.GameClosest, server.GameClosest, err)
	} else if len(lib.Questions) == 0 {
		logger.Printf("no %s question packs yet; %s games cannot start", server.GameClosest, server.GameClosest)
	}
	srv, err := server.New(server.Config{
		Engine: server.RealEngine{},
		Engines: map[string]server.Engine{
			server.GamePoker:   server.PokerEngine{},
			server.GameLudo:    server.LudoEngine{},
			server.GameQuiz:    server.QuizEngine{},
			server.GameClosest: server.ClosestEngine{},
		},
		AI:          server.RealAI{},
		Bots:        server.RealBots,
		PokerBots:   server.RealPokerBots(server.PokerBotConfig{External: external}),
		PokerLevels: pokerLevels,
		Assets:      estate.Assets,
		DataDir:     *dataDir,
		PublicURL:   strings.TrimRight(*publicURL, "/"),
		LANURL:      lanURL,
		Version:     version,
		Logger:      logger,
	})
	if err != nil {
		listener.Close()
		return err
	}
	// Timeouts stop slow clients from holding connections open (a body sent a
	// byte at a time, a response never read). WebSockets clear these deadlines
	// before they are hijacked (server.handleSocket), so they live on.
	httpServer := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          logger,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 1)
	go func() { errs <- httpServer.Serve(listener) }()

	localURL := fmt.Sprintf("http://localhost:%d/", port)
	logger.Printf("Estate %s listening on %s", version, listener.Addr())
	logger.Printf("table screen: %s", localURL)
	logger.Printf("phones on this network: %s/play", lanURL)
	if *publicURL != "" {
		logger.Printf("public: %s", strings.TrimRight(*publicURL, "/"))
	}
	if *dataDir != "" {
		logger.Printf("data: %s", *dataDir)
	}
	if !*noOpen {
		if err := openTable(localURL); err != nil {
			logger.Printf("could not open a browser (%v); open %s yourself", err, localURL)
		}
	}

	select {
	case err := <-errs:
		srv.Close()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	logger.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := httpServer.Shutdown(shutdownCtx)
	if err := srv.Close(); err != nil {
		logger.Printf("saving data: %v", err)
	}
	if shutdownErr != nil && !errors.Is(shutdownErr, context.DeadlineExceeded) {
		return shutdownErr
	}
	return nil
}
