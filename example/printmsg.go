package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"

	"github.com/Arman92/go-tdlib"
	"github.com/cdtj/bottalker-go"
)

var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to `file`")
var memprofile = flag.String("memprofile", "", "write memory profile to `file`")

// Run for test purposes
func Run() string {
	main()
	return ""
}

func main() {
	flag.Parse()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal("could not create CPU profile: ", err)
		}
		defer f.Close() // error handling omitted for example
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal("could not start CPU profile: ", err)
		}
		defer pprof.StopCPUProfile()
	}

	replies := make(chan *tdlib.TdMessage)
	botsToCheck := []*bottalker.Bot{
		{
			Label:       "QBot",
			ChatID:      int64(600120108),
			ChkInterval: time.Second * 30,
			Replies:     replies,
			Commands: []bottalker.BotCommandType{
				&bottalker.BotCommandPayload{
					BotCommand: bottalker.BotCommand{
						Data: []byte("/bal_btc"),
					},
				},
				&bottalker.BotCommandPayload{
					BotCommand: bottalker.BotCommand{
						Data: []byte("/bal_gst"),
					},
				},
			},
		},
	}
	log.Println("Starting clients")
	wg := &sync.WaitGroup{}

	wg.Add(1)
	go func(*sync.WaitGroup, []*bottalker.Bot) {
		defer wg.Done()
		clientWithProxy("checker", botsToCheck)
	}(wg, botsToCheck)

	go func(<-chan *tdlib.TdMessage) {
		for msg := range replies {
			log.Printf("Incoming message [%T]:\n", *msg)
			switch (*msg).(type) {
			case *tdlib.UpdateMessageContent:
				log.Printf("\t%+v\n", (*msg).(*tdlib.UpdateMessageContent).NewContent)
			case *tdlib.UpdateMessageEdited:
				log.Printf("\t%+v\n", (*msg).(*tdlib.UpdateMessageEdited).ReplyMarkup)
			case *tdlib.UpdateChatLastMessage:
				log.Printf("\t%+v\n\nPrinting Message:\n\n", (*msg).(*tdlib.UpdateChatLastMessage).LastMessage)
				bottalker.PrintMessage((*msg).(*tdlib.UpdateChatLastMessage).LastMessage)
			}
		}
	}(replies)

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal("could not create memory profile: ", err)
		}
		defer f.Close() // error handling omitted for example
		runtime.GC()    // get up-to-date statistics
		if err := pprof.WriteHeapProfile(f); err != nil {
			log.Fatal("could not write memory profile: ", err)
		}
	}

	wg.Wait()
}

func clientWithProxy(id string, bots []*bottalker.Bot) {
	log.Println("\tStarting:", id)
	tgLogPath := fmt.Sprintf("./logs/tg_%s.log", id)

	bt := &bottalker.Bottalker{
		TelegramLog: &tgLogPath,
		TelegramClient: &bottalker.TelegramClient{
			ID: id,
			Config: &tdlib.Config{
				APIID:   "859145",
				APIHash: "0515b7132ab667dc75a8eb71485ec27c",
			},
		},
		Bots: bots,
	}

	bt.Run()
}
