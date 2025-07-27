package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/csmith/envflag/v2"
	"github.com/csmith/slogflags"
	"github.com/greboid/ircparser/client"
	"github.com/greboid/ircparser/parser"
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
	cli := client.NewClient(context.Background(),
		parser.NewConnectionConfig(*hostname,
			parser.WithPort(*port),
			parser.WithTLS(*tls),
			parser.WithNick(*nickname),
			parser.WithUsername(*username),
			parser.WithSASL(*saslusername, *saslpassword),
		),
	)
	channels := 0
	cli.Subscribe(parser.EventJoin, func(event *parser.Event) {
		if joinData, ok := event.Data.(*parser.JoinData); ok {
			fmt.Println("Joined: " + joinData.Nick + " " + joinData.Channel)
			if joinData.Nick == cli.GetCurrentNick() {
				channels++
			}
		}
	})
	go func() {
		time.Sleep(5 * time.Second)
		if len(cli.GetChannels()) == 0 && *joinChannel != "" {
			cli.Join(*joinChannel)
		}
	}()
	err := cli.Start()
	if err != nil {
		panic(err)
	}
	defer cli.Stop()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	fmt.Printf("Quit\n")
}
