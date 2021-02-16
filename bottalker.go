package bottalker

import (
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Arman92/go-tdlib"
	"github.com/imdario/mergo"
)

// Bottalker is the main struct of bottalker app
type Bottalker struct {
	TelegramClient   *TelegramClient // telegram client settings struct; Can be generated via wizard, leave blank for wizard prompt
	Bots             []*Bot          // array of bot struct for telegram message; Can be generated via wizard, leave blank for wizard prompt
	TelegramLog      *string         // full path to tdlib log; Example: `./logs/tdlib.log`, default is stdout
	TelegralLogLevel int             // verbosity level for tdlib, default is 5; Check `tdlib/SetLogVerbosityLevel` for more info
	TalkerLog        *string         // full path to bottalker-go log; Example `./logs/talker.log`, default is stdout
	ErrChan          chan *BotError  // errors channel; Specify and handle *BotError channel if you want to. `bottalker-go/defaultErrorHandler` will be used if not specified
	wg               *sync.WaitGroup // holds thread until bots stop
}

// Run is running bottalker instance
func (bt *Bottalker) Run() {
	if bt.TelegralLogLevel > 0 {
		tdlib.SetLogVerbosityLevel(bt.TelegralLogLevel)
	}

	if bt.TelegramLog != nil {
		logPath := "./logs/telegram.log"
		if *bt.TelegramLog != "" {
			logPath = *bt.TelegramLog
		}
		createDir(logPath)
		tdlib.SetFilePath(logPath)
	}

	if bt.TalkerLog != nil {
		logPath := "./logs/bottalker.log"
		if *bt.TalkerLog != "" {
			logPath = *bt.TalkerLog
		}
		f, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Panicf("Error opening file: %v", err)
		}
		defer f.Close()
		log.SetOutput(f)
	}

	// This is working client config
	// Those fields will be merged with your config
	clientConfig := tdlib.Config{
		APIID:                  "859145",                           // better (for me) if you use your own `APIID`, you can get it on: https://my.telegram.org
		APIHash:                "0515b7132ab667dc75a8eb71485ec27c", // better (for me) if you use your own `APIHash`, you can get it on: https://my.telegram.org
		SystemLanguageCode:     "en",
		DeviceModel:            "Desktop",
		SystemVersion:          "0.0.1",
		ApplicationVersion:     "0.0.1",
		UseFileDatabase:        true,
		UseMessageDatabase:     false,
		UseChatInfoDatabase:    false,
		UseTestDataCenter:      false,
		DatabaseDirectory:      fmt.Sprintf("./clients/%s/tdlib-db", bt.TelegramClient.ID),
		FileDirectory:          fmt.Sprintf("./clients/%s/tdlib-files", bt.TelegramClient.ID),
		IgnoreFileNames:        false,
		EnableStorageOptimizer: true,
	}

	// Merging your config with default
	if err := mergo.Merge(&clientConfig, bt.TelegramClient.Config); err != nil {
		log.Panic("Unable to build config: ", err)
	}

	bt.TelegramClient.client = tdlib.NewClient(clientConfig)

	// Handle Ctrl+C, Gracefully exit and shutdown tdlib
	// Actaully I'm not sure how graceful it is
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		bt.TelegramClient.client.DestroyInstance()
		os.Exit(0)
	}()

	err := bt.connect()
	if err != nil {
		log.Println("Unable to start app:", err)
	}

	// We always need to get chat list first, even if we're accessing conversation via ID
	// It's telegram logic
	err = bt.TelegramClient.initChatList()
	if err != nil {
		log.Println("Unable to start app:", err)
		return
	}

	// Hadling errors in your chan if you have it
	if bt.ErrChan == nil {
		bt.ErrChan = make(chan *BotError)
		go bt.defaultErrorHandler()
	}

	bt.wg = &sync.WaitGroup{}

	bt.startWorkers()
}

// defaultErrorHandler log errors received in error chan
func (bt *Bottalker) defaultErrorHandler() {
	log.Printf("%s > Starting defaultErrorHandler", bt.TelegramClient.ID)
	for bErr := range bt.ErrChan {
		switch bErr.ErrType {
		case BotErrFatal:
			log.Printf("Terminated: %s", bErr)
			time.Sleep(30 * time.Second)
		case BotErrWarn:
			log.Printf("Warned with: %s", bErr)
		case BotErrError:
			log.Printf("Error: %s", bErr)
			time.Sleep(30 * time.Second)
		default:
			log.Printf("Unknown error [%v]: %s", bErr.ErrType, bErr)
		}
	}
}

// initMessageHandler filter messages and process them to `replies` channel
// I had plans to make this customizable but those settings should fit most cases
func (bt *Bottalker) initMessageHandler() {
	log.Printf("%s > Starting messageHandler", bt.TelegramClient.ID)
	for _, b := range bt.Bots {
		if b.Replies == nil {
			continue
		}
		eventFilter := func(msg *tdlib.TdMessage) bool {
			switch (*msg).(type) {
			case *tdlib.UpdateMessageContent:
				if (*msg).(*tdlib.UpdateMessageContent).ChatID == b.ChatID {
					return true
				}
			case *tdlib.UpdateMessageEdited:
				if (*msg).(*tdlib.UpdateMessageEdited).ChatID == b.ChatID {
					return true
				}
			case *tdlib.UpdateChatLastMessage:
				if (*msg).(*tdlib.UpdateChatLastMessage).ChatID == b.ChatID {
					return true
				}
			}
			return false
		}
		// This 3 messages happens on InlineKeyboardButton trigger
		msgInstances := []tdlib.TdMessage{
			&tdlib.UpdateMessageContent{},
			&tdlib.UpdateMessageEdited{},
			&tdlib.UpdateChatLastMessage{},
		}
		for _, msgInstance := range msgInstances {
			go func(b *Bot, msgInstance tdlib.TdMessage) {
				for newMsg := range bt.TelegramClient.client.AddEventReceiver(msgInstance, eventFilter, 100).Chan {
					b.Replies <- &newMsg
				}
			}(b, msgInstance)
		}
	}
}

// connect initializes Telegram client session
func (bt *Bottalker) connect() error {
	for _, proxy := range bt.TelegramClient.Proxies {
		bt.TelegramClient.client.AddProxy(proxy.Server, proxy.Port, proxy.Enable, proxy.TypeParam)
	}

AUTHLOOP:
	for {
		currentState, _ := bt.TelegramClient.client.Authorize()
		log.Println(bt.TelegramClient.ID, "> Authorization State:", currentState.GetAuthorizationStateEnum())
		switch currentState.GetAuthorizationStateEnum() {
		case tdlib.AuthorizationStateWaitEncryptionKeyType:
			continue
		case tdlib.AuthorizationStateReadyType:
			return nil
		default:
			break AUTHLOOP
		}
	}

	var yesNo string
	for {
		switch strings.ToLower(yesNo) {
		case "y":
			err := bt.wizard(WizardClient)
			if err != nil {
				return err
			}
			return nil
		case "n":
			return fmt.Errorf("%s > Authorization canceled", bt.TelegramClient.ID)
		default:
			fmt.Printf("%s > You're not authorized, do you want to run wizard? [y/n]: ", bt.TelegramClient.ID)
			fmt.Scanln(&yesNo)
		}
	}
}

// startWorkers starting workers
func (bt *Bottalker) startWorkers() {
	log.Printf("%s > Starting startWorkers", bt.TelegramClient.ID)

	// initializing message handler first
	bt.initMessageHandler()

	for _, b := range bt.Bots {
		b.initBot(bt.TelegramClient, bt.ErrChan)
		bt.wg.Add(1)
		// TODO: Reporting bot status, not sure that we really need it
		/*
			go func(b *Bot) {
				log.Println("Reporting in:", b.RepInterval)
				c := time.Tick(b.RepInterval)
				for nextTick := range c {
					log.Println("Repored on:", nextTick)
				}
			}(b)
		*/
		go b.run(bt.ErrChan)
	}
	bt.wg.Wait()
}

// WizardTargetEnum is enum to select desired wizard
type WizardTargetEnum int

// Enum to switch wizard targets
const (
	WizardClient WizardTargetEnum = iota // to configure client only
	WizardBot                            // to configure bot only
	WizardBoth                           // to configure both of them
)

func (bt *Bottalker) wizard(target WizardTargetEnum) error {
	switch target {
	case WizardClient:
		tc := bt.TelegramClient
		for {
			currentState, _ := tc.client.Authorize()
			if currentState.GetAuthorizationStateEnum() == tdlib.AuthorizationStateWaitPhoneNumberType {
				fmt.Printf("%s > Enter phone: ", tc.ID)
				var number string
				fmt.Scanln(&number)
				_, err := tc.client.SendPhoneNumber(number)
				if err != nil {
					return fmt.Errorf("%s > Error sending phone number: %v", tc.ID, err)
				}
			} else if currentState.GetAuthorizationStateEnum() == tdlib.AuthorizationStateWaitCodeType {
				fmt.Printf("%s > Enter code: ", tc.ID)
				var code string
				fmt.Scanln(&code)
				_, err := tc.client.SendAuthCode(code)
				if err != nil {
					return fmt.Errorf("%s > Error sending auth code : %v", tc.ID, err)
				}
			} else if currentState.GetAuthorizationStateEnum() == tdlib.AuthorizationStateWaitPasswordType {
				fmt.Printf("%s > Enter Password: ", tc.ID)
				var password string
				fmt.Scanln(&password)
				_, err := tc.client.SendAuthPassword(password)
				if err != nil {
					return fmt.Errorf("%s > Error sending auth password: %v", tc.ID, err)
				}
			} else if currentState.GetAuthorizationStateEnum() == tdlib.AuthorizationStateReadyType {
				fmt.Printf("%s > Authorization Ready! Let's rock", tc.ID)
				return nil
			} else if currentState.GetAuthorizationStateEnum() == tdlib.AuthorizationStateLoggingOutType {
				return fmt.Errorf("%s > You have been logged out, clear dbfiles to reinit account", tc.ID)
			}
		}
	}
	return nil
}

// TelegramClient is used to define client details
type TelegramClient struct {
	ID      string         // identifies clients including stored ones, so don't change between runs
	client  *tdlib.Client  // instance of connected client goes here
	Config  *tdlib.Config  // tdlib.Config, we have kinda working config with default values so you need to specify at least `APIID` and `APIHash`, ah, okay, you can specify nothig and use mine API related vals
	Proxies []*ClientProxy // array of ClientProxiy; use them if telegram server can't be reached directly
}

// ClientProxy used to define proto/socks/http proxy
type ClientProxy struct {
	Server    string          // server dns
	Port      int32           // port
	Enable    bool            // if true proxy will be used and added to proxies queue
	TypeParam tdlib.ProxyType // tdlib.ProxyType
}

func (tc *TelegramClient) getHistory(chatID int64) error {
	lastbmsg, err := tc.getLastMsgID(chatID)
	if err != nil {
		return err
	}
	var bmsgLimit int32 = 8
LOOP:
	for {
		bmsgs, err := tc.client.GetChatHistory(chatID, lastbmsg, 0, bmsgLimit, false)
		if err != nil {
			return fmt.Errorf("GetChatHistory failed: %v", err)
		}
		for _, bmsg := range bmsgs.Messages {
			lastbmsg = bmsg.ID
		}
		if len(bmsgs.Messages) < int(bmsgLimit-1) {
			break LOOP
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (tc *TelegramClient) initChatList() error {
	chatLimit := 100
	allChats := make([]*tdlib.Chat, 0, chatLimit)
	var haveFullChatList bool
	err := tc.fetchChats(chatLimit, haveFullChatList, &allChats)
	if err != nil {
		return fmt.Errorf("fetchChats failed: %s", err)
	}

	log.Printf("%s > Got %d chats\n", tc.ID, len(allChats))
	for k, chat := range allChats {
		log.Printf("\t%d > Chat title: %s [%d]", k, chat.Title, chat.ID)
	}

	return nil
}

func (tc *TelegramClient) fetchChats(limit int, haveFullChatList bool, allChats *[]*tdlib.Chat) error {
	if !haveFullChatList && limit > len(*allChats) {
		offsetOrder := int64(math.MaxInt64)
		offsetChatID := int64(0)
		var lastChat *tdlib.Chat

		if len(*allChats) > 0 {
			lastChat = (*allChats)[len(*allChats)-1]
			offsetOrder = int64(lastChat.Positions[0].Order)
			offsetChatID = lastChat.ID
		}

		chats, err := tc.client.GetChats(nil, tdlib.JSONInt64(offsetOrder),
			offsetChatID, int32(limit-len(*allChats)))
		if err != nil {
			return err
		}
		if len(chats.ChatIDs) == 0 {
			haveFullChatList = true
			return nil
		}

		for _, chatID := range chats.ChatIDs {
			// get chat info from tdlib
			chat, err := tc.client.GetChat(chatID)
			if err == nil {
				*allChats = append(*allChats, chat)
			} else {
				return err
			}
		}
		return tc.fetchChats(limit, haveFullChatList, allChats)
	}
	return nil
}

func (tc *TelegramClient) getLastMsgID(chatID int64) (int64, error) {
	chat, err := tc.client.GetChat(chatID)
	if err != nil {
		return int64(0), fmt.Errorf("GetChat faield: %v", err)
	}

	if chat.LastMessage != nil {
		bmsg := chat.LastMessage
		return bmsg.ID, nil
	}

	return int64(0), nil
}

func (tc *TelegramClient) getMsgByDate(chatID int64, date int32, print bool) (*tdlib.Message, error) {
	bmsg, err := tc.client.GetChatMessageByDate(chatID, date)
	if err != nil {
		return nil, fmt.Errorf("GetChatMessageByDate failed: %s", err)
	}

	return bmsg, nil
}

// MessageButton used to represent valuable attrs of bot keyboard buttons
type MessageButton struct {
	Text    string // Button caption
	Payload []byte // Button command
}

// PrintMessage displays message content
func PrintMessage(msg *tdlib.Message) {
	log.Printf("Printing Message [%d @ %v]", msg.ID, time.Unix(int64(msg.Date), 0))
	log.Printf("Text: %s", *GetMessageText(msg))
	log.Println("Buttons:")
	for _, btn := range GetMessageButtons(msg) {
		log.Printf(`	%s: "%s"`, btn.Text, btn.Payload)
	}
}

// GetMessageText returns plain-text from Telegram message
func GetMessageText(msg *tdlib.Message) *string {
	switch msg.Content.(type) {
	case *tdlib.MessageText:
		content, ok := msg.Content.(*tdlib.MessageText)
		if ok {
			return &content.Text.Text
		}
	}
	return nil
}

// GetMessageButtons parses and returns array of MessageButton from Telegram message
func GetMessageButtons(msg *tdlib.Message) []*MessageButton {
	if msg.ReplyMarkup != nil {
		var buttons []*MessageButton = make([]*MessageButton, 0)
		rm := msg.ReplyMarkup
		switch rm.(type) {
		case *tdlib.ReplyMarkupShowKeyboard:
			// I don't have an example with that kind of bot,
			// skipped for now
			/*
				for i, row := range rm.(*tdlib.ReplyMarkupShowKeyboard).Rows {
					for j, col := range row {
						// buttons = append(buttons, decodeMessageButton(col))
					}
				}
			*/
		case *tdlib.ReplyMarkupInlineKeyboard:
			for _, row := range rm.(*tdlib.ReplyMarkupInlineKeyboard).Rows {
				for _, col := range row {
					buttons = append(buttons, decodeMessageButton(&col))
				}
			}
		}
		return buttons
	}
	return nil
}

// TODO: Add KeyboardButton
func decodeMessageButton(keyboardButton *tdlib.InlineKeyboardButton) *MessageButton {
	messageButton := &MessageButton{
		Text: keyboardButton.Text,
	}
	switch keyboardButton.Type.(type) {
	case *tdlib.InlineKeyboardButtonTypeCallback:
		messageButton.Payload = keyboardButton.Type.(*tdlib.InlineKeyboardButtonTypeCallback).Data
	case *tdlib.InlineKeyboardButtonTypeCallbackWithPassword:
		messageButton.Payload = keyboardButton.Type.(*tdlib.InlineKeyboardButtonTypeCallbackWithPassword).Data
	case *tdlib.InlineKeyboardButtonTypeSwitchInline:
		messageButton.Payload = []byte(keyboardButton.Type.(*tdlib.InlineKeyboardButtonTypeSwitchInline).Query)
	}
	return messageButton
}

// Creates dirs by full path
func createDir(path string) {
	if stats, err := os.Stat(path); os.IsNotExist(err) {
		dir, err := filepath.Abs(filepath.Dir(path))
		if err != nil {
			fmt.Println("err:", err)
		}
		err = os.MkdirAll(dir, 0700)
		if err != nil {
			fmt.Println("err:", err)
		}
	} else {
		fmt.Println(stats)
	}
}
