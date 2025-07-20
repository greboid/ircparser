package main

import (
	"context"
	"fmt"
	"github.com/csmith/envflag/v2"
	"github.com/csmith/slogflags"
	"github.com/greboid/ircparser"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	envflag.Parse()
	slogflags.Logger(
		slogflags.WithCustomLevels(map[string]slog.Level{"TRACE": parser.LogTrace}),
		slogflags.WithSetDefault(true),
	)
	conf := parser.NewConnectionConfig("irc.quakenet.org", 6667, false)
	conf.Nick = "Greparser"
	conf.Username = "Greboid"
	conf.SASLUser = ""
	conf.SASLPass = ""
	conf.TLS = false
	con := parser.NewParser(context.Background(), conf)
	err := con.Start()
	if err != nil {
		panic(err)
	}
	defer con.Stop()
	con.Subscribe(parser.EventRegistered, func(event *parser.Event) {
		con.Join("#noshame")
	})
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	fmt.Printf("Quit\n")
}
