package bottalker

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Arman92/go-tdlib"
)

// Bot is bot
type Bot struct {
	Label          string                  // friendly name
	ChatID         int64                   // Telegram chat id
	ChkInterval    time.Duration           // delay interval between checks
	Commands       []BotCommandType        // bot commands to be sent by interval
	Replies        chan<- *tdlib.TdMessage // channel to receive replies as Telegram message
	TelegramClient *TelegramClient         // parent struct that holds Telegram client
	ticker         *time.Ticker            // ticker is here to make it stop
	sync.RWMutex
	// TODO: those guys are not implemented yet
	cooldown    time.Duration // cooldown if you need a break, use `SetCooldown` to increase it
	NotifyID    int64         // notify telegram chat (contact, bot, group, whatever) on kind of event
	LogID       int64         // same as `NotifyID` but idea is to send log or report
	RepInterval time.Duration // reporting interval
}

func (b *Bot) initBot(tc *TelegramClient, errCh chan<- *BotError) {
	log.Println("Initing bot:", b.Label)
	tc.getHistory(b.ChatID)
	b.TelegramClient = tc
	b.ticker = time.NewTicker(b.ChkInterval)

	for _, bc := range b.Commands {
		bc.setBot(b)
		switch bc.(type) {
		case *BotCommandPayload:
			bcp, ok := bc.(*BotCommandPayload)
			if !ok {
				errCh <- &BotError{
					Err:         fmt.Errorf("Unable to assert bot command as payload"),
					ErrType:     BotErrFatal,
					Bot:         b,
					CommandType: bc,
				}
				continue
			}
			log.Printf("\t[%s] starting for: %s\n", b.Label, bcp.Data)

			bcp.setPayload()
			if bcp.MsgID == 0 {
				log.Println("\t\tbmsgID is not defined, trying to use latest message")
				bmsg, err := tc.getMsgByDate(b.ChatID, int32(time.Now().Unix()), false)
				if err != nil {
					errCh <- &BotError{
						Err:         fmt.Errorf("Unable to get latest message: %v", err),
						ErrType:     BotErrFatal,
						Bot:         b,
						CommandType: bcp,
					}
					continue
				} else {
					bcp.MsgID = bmsg.ID
					log.Println("\t\tbmsgID = ", bcp.MsgID)
				}
			}
			if !bcp.Passive {
				bcp.start()
			}
		case *BotCommandChat:
			bcc, ok := bc.(*BotCommandChat)
			if !ok {
				errCh <- &BotError{
					Err:         fmt.Errorf("Unable to assert bot command as chat"),
					ErrType:     BotErrFatal,
					Bot:         b,
					CommandType: bc,
				}
				continue
			}
			log.Printf("\t[%s] starting for: %s\n", b.Label, bcc.Data)

			_, err := tc.getMsgByDate(b.ChatID, int32(time.Now().Unix()), false)
			if err != nil {
				errCh <- &BotError{
					Err:         fmt.Errorf("Unable to get latest message: %v", err),
					ErrType:     BotErrFatal,
					Bot:         b,
					CommandType: bcc,
				}
				continue
			}
			if !bcc.Passive {
				bcc.start()
			}
		}
	}
}

// TODO: implement bot status report
func (b *Bot) genReport(client *tdlib.Client, errCh chan<- BotError) {
	log.Println("genReport called for:", b)
}

// getCommands returns array of active bot commands
func (b *Bot) getCommands() []BotCommandType {
	bcts := make([]BotCommandType, 0)
	for _, bct := range b.Commands {
		if bct.isRunning() {
			bcts = append(bcts, bct)
		}
	}
	return bcts
}

// getAlive checks if bot has any active commands to perform
func (b *Bot) getAlive() bool {
	if len(b.getCommands()) > 0 {
		return true
	}
	return false
}

// run runs ticker to trigger bot commands by interval
//
// this doesnt looks like thread safe but as this the only one thread
// which will interact with BotCommands, I hope it should be ok
func (b *Bot) run(errCh chan<- *BotError) {
	tickerPos := 0
	for {
		select {
		case t := <-b.ticker.C:
			log.Println("Checked on:", t)
			/*
				cd := b.GetCooldown()
				if cd > 0 {
					time.Sleep(cd)
					b.SetCooldown(0)
				}
			*/
			bc := b.getCommands()
			if tickerPos >= len(bc) {
				tickerPos = 0
				for _, botCommands := range b.getCommands() {
					botCommands.stop()
				}
			}
			log.Printf("%d > quering: %v", tickerPos, bc[tickerPos])
			_, bErr := bc[tickerPos].Trigger()
			tickerPos++
			if bErr != nil {
				errCh <- bErr
			}
		}
	}
}

// TODO: Implement cooldown login

// SetCooldown adds timeout cooldown before next command trigger
func (b *Bot) SetCooldown(d time.Duration) {
	b.Lock()
	defer b.Unlock()
	b.cooldown = d
}

// GetCooldown adds timeout cooldown before next command trigger
func (b *Bot) GetCooldown() time.Duration {
	b.Lock()
	defer b.Unlock()
	return b.cooldown
}

// BotError contains everything about error caused to bot
type BotError struct {
	Bot         *Bot
	CommandType BotCommandType
	Err         error
	ErrType     BotErrorTypeEnum
}

// BotErrorTypeEnum is bot error type code
type BotErrorTypeEnum int

// Enum to switch between bot error types
const (
	BotErrWarn  BotErrorTypeEnum = iota // bot warns something
	BotErrError                         // bot errored
	BotErrFatal                         // bot crashes
)

func (bErr *BotError) Error() (errMsg string) {
	if bErr.Bot != nil {
		errMsg += fmt.Sprintf("%s > ", bErr.Bot.Label)
	}
	if bErr.CommandType != nil {
		errMsg += fmt.Sprintf("[%s]: ", bErr.CommandType.getData())
	}
	if bErr.Err != nil {
		errMsg += bErr.Err.Error()
	}
	return
}

// BotCommandType is bot interaction type
type BotCommandType interface {
	start() // set isRunning
	stop()  // unset isRunning

	setBot(b *Bot)                        // define parent
	Trigger() (*tdlib.Message, *BotError) // trigerring command, the way to interract with passive commands but can used with any
	isRunning() bool                      // return running state
	getData() []byte                      // get command data
}

// BotCommandPayload interacting with inline keyboard
type BotCommandPayload struct {
	MsgID       int64                           // Telegram message ID which contains Keyboard Markup
	payloadData *tdlib.CallbackQueryPayloadData // will be generated form `BotCommand.Data`
	BotCommand
}

// BotCommandChat sending command as chat message
type BotCommandChat struct {
	BotCommand
}

// BotCommand is message
type BotCommand struct {
	Data    []byte // Data will be send as payload to bot
	Passive bool   // Set passive for bot commands that will be triggered manually by `BotCommand.Trigger()`
	running bool   // can be stopper and started by `BotCommand.stop()` and `BotCommand.start()`
	bot     *Bot   // parent struct
	sync.RWMutex
	// TODO: Part of reporting functionality
	reportData string // Will contain report data
}

// start adding command to queue
func (bc *BotCommand) start() {
	bc.Lock()
	defer bc.Unlock()
	bc.running = true
}

// stop removing command from queue
func (bc *BotCommand) stop() {
	bc.Lock()
	defer bc.Unlock()
	bc.running = false
}

// getData returns data which will be sent to Telegram message's InlineKeyboardButton
func (bc *BotCommand) getData() []byte {
	return bc.Data
}

// setBot links BotCommand with Bot
func (bc *BotCommand) setBot(b *Bot) {
	bc.bot = b
}

// isRunning returns current run state
func (bc *BotCommand) isRunning() bool {
	bc.Lock()
	defer bc.Unlock()
	return bc.running
}

// setPayload sets payload from Data
func (bcp *BotCommandPayload) setPayload() {
	bcp.Lock()
	defer bcp.Unlock()
	bcp.payloadData = tdlib.NewCallbackQueryPayloadData(bcp.Data)
}

// Trigger interacting with bot
func (bcp *BotCommandPayload) Trigger() (*tdlib.Message, *BotError) {
	_, err := bcp.bot.TelegramClient.client.GetCallbackQueryAnswer(bcp.bot.ChatID, bcp.MsgID, bcp.payloadData)
	if err != nil {
		switch err.Error() {
		case "timeout":
			return nil, &BotError{
				Err:         fmt.Errorf("GetCallbackQueryAnswer [%v] failed: %s", bcp.Data, err),
				ErrType:     BotErrWarn,
				Bot:         bcp.bot,
				CommandType: bcp,
			}
		default:
			return nil, &BotError{
				Err:         fmt.Errorf("GetCallbackQueryAnswer [%v] failed: %s", bcp.Data, err),
				ErrType:     BotErrError,
				Bot:         bcp.bot,
				CommandType: bcp,
			}
		}
	}
	m, err := bcp.bot.TelegramClient.client.GetMessage(bcp.bot.ChatID, bcp.MsgID)
	if err != nil {
		return nil, &BotError{
			Err:         fmt.Errorf("GetMessage [%d] failed: %s", bcp.MsgID, err),
			ErrType:     BotErrError,
			Bot:         bcp.bot,
			CommandType: bcp,
		}
	}
	return m, nil
}

// Trigger performin a query
func (bcc *BotCommandChat) Trigger() (*tdlib.Message, *BotError) {
	m, err := bcc.bot.TelegramClient.client.SendMessage(bcc.bot.ChatID, int64(0), int64(0), tdlib.NewMessageSendOptions(false, false, nil), nil,
		tdlib.NewInputMessageText(
			tdlib.NewFormattedText(string(bcc.Data), nil),
			true,
			true,
		),
	)
	if err != nil {
		return nil, &BotError{
			Err:         fmt.Errorf("SendMessage [%s] failed: %s", bcc.Data, err),
			ErrType:     BotErrFatal,
			Bot:         bcc.bot,
			CommandType: bcc,
		}
	}
	return m, nil
}
