package ircclient

import (
	"fmt"
	"strings"
	"time"

	"github.com/ergochat/irc-go/ircmsg"
)

// EventKind says what an Event describes.
type EventKind int

const (
	EventMessage EventKind = iota // PRIVMSG or NOTICE, live or from history
	EventJoin
	EventPart
	EventQuit
	EventHistoryEnd // end of a CHATHISTORY batch for Target
	EventError      // FAIL or ERROR from the server
)

// Event is something the server sent during an interactive session.
type Event struct {
	Kind    EventKind
	Message Message
	// History is true for messages replayed by CHATHISTORY.
	History bool
}

// Join joins channel.
func (c *Client) Join(channel string) error { return c.writeLine("JOIN " + channel) }

// RequestHistory asks for the latest messages in target. Results arrive as
// History events from Listen, followed by EventHistoryEnd.
func (c *Client) RequestHistory(target string, limit int) error {
	if limit <= 0 || (c.HistoryLimit > 0 && limit > c.HistoryLimit) {
		limit = c.HistoryLimit
	}
	if limit <= 0 {
		return fmt.Errorf("server did not advertise CHATHISTORY")
	}
	return c.writeLine(fmt.Sprintf("CHATHISTORY LATEST %s * %d", target, limit))
}

// Listen reads from the server until the connection closes, calling handle
// for each event. It must not run at the same time as History or Quit.
func (c *Client) Listen(handle func(Event)) error {
	batches := map[string]string{} // batch id -> target
	for {
		msg, err := c.readMessageNoDeadline()
		if err != nil {
			return err
		}
		ev := Message{Nick: msg.Nick(), Target: param(msg, 0), Text: param(msg, 1), Time: time.Now()}
		if ok, ts := msg.GetTag("time"); ok {
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				ev.Time = t
			}
		}
		_, batch := msg.GetTag("batch")
		switch msg.Command {
		case "BATCH":
			ref := param(msg, 0)
			if id, ok := strings.CutPrefix(ref, "+"); ok {
				batches[id] = param(msg, 2)
			} else if id, ok := strings.CutPrefix(ref, "-"); ok {
				if target, known := batches[id]; known {
					delete(batches, id)
					handle(Event{Kind: EventHistoryEnd, Message: Message{Target: target}})
				}
			}
		case "PRIVMSG", "NOTICE":
			_, inBatch := batches[batch]
			handle(Event{Kind: EventMessage, Message: ev, History: inBatch})
		case "JOIN":
			handle(Event{Kind: EventJoin, Message: ev})
		case "PART":
			handle(Event{Kind: EventPart, Message: ev})
		case "QUIT":
			ev.Text = param(msg, 0)
			handle(Event{Kind: EventQuit, Message: ev})
		case "FAIL", "ERROR":
			ev.Text = strings.Join(msg.Params, " ")
			handle(Event{Kind: EventError, Message: ev})
		}
	}
}

func (c *Client) readMessageNoDeadline() (ircmsg.Message, error) {
	for {
		c.conn.SetReadDeadline(time.Time{})
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return ircmsg.Message{}, err
		}
		msg, err := ircmsg.ParseLine(line)
		if err != nil {
			continue
		}
		if msg.Command == "PING" {
			if err := c.writeLine("PONG :" + param(msg, 0)); err != nil {
				return ircmsg.Message{}, err
			}
			continue
		}
		return msg, nil
	}
}
