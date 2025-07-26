package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/csmith/envflag/v2"
	"github.com/csmith/slogflags"
	"github.com/greboid/ircparser"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	nickname     = flag.String("nickname", "", "")
	username     = flag.String("username", "", "")
	hostname     = flag.String("hostname", "", "")
	port         = flag.Int("port", 6697, "")
	tls          = flag.Bool("tls", true, "")
	saslusername = flag.String("saslusername", "", "")
	saslpassword = flag.String("saslpassword", "", "")
	joinChannel  = flag.String("joinchannel", "", "")
)

func main() {
	envflag.Parse()
	slogflags.Logger(
		slogflags.WithCustomLevels(map[string]slog.Level{"TRACE": parser.LogTrace}),
		slogflags.WithSetDefault(true),
	)
	if *nickname == "" || *hostname == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}
	if *username == "" {
		*username = *nickname
	}
	conf := parser.NewConnectionConfig(*hostname, *port, *tls)
	conf.Nick = *nickname
	conf.Username = *username
	conf.SASLUser = *saslusername
	conf.SASLPass = *saslpassword
	con := parser.NewParser(context.Background(), conf)
	channels := 0
	con.Subscribe(parser.EventJoin, func(event *parser.Event) {
		if joinData, ok := event.Data.(*parser.JoinData); ok {
			fmt.Println("Joined: " + joinData.Nick + " " + joinData.Channel)
		}
	})
	go func() {
		time.Sleep(5 * time.Second)
		if channels == 0 && *joinChannel != "" {
			con.Join(*joinChannel)
		}
	}()
	err := con.Start()
	if err != nil {
		panic(err)
	}
	defer con.Stop()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	fmt.Printf("Quit\n")
}
