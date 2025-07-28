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
	"strings"
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
	cli.Subscribe(parser.EventNamesComplete, func(event *parser.Event) {
		if data, ok := event.Data.(*parser.NamesCompleteData); ok {
			userNames := make([]string, 0, len(data.Channel.GetUsers()))
			for _, user := range data.Channel.GetUsers() {
				prefixes := user.GetPrefixString(cli.GetChannelPrefixes())
				userNames = append(userNames, fmt.Sprintf("%s%s", prefixes, user.Nick))
			}
			fmt.Printf("%s:= %s\n", data.Channel.Name, strings.Join(userNames, ", "))
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
