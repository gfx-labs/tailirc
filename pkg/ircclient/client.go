// Package ircclient is a minimal IRC client used by the send and read
// commands. It speaks only what tailirc needs: registration, PRIVMSG and
// IRCv3 CHATHISTORY.
package ircclient

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ergochat/irc-go/ircmsg"
)

// Message is a channel message returned by History.
type Message struct {
	Time   time.Time
	Nick   string
	Target string
	Text   string
}

// Client is a registered IRC connection.
type Client struct {
	conn         net.Conn
	reader       *bufio.Reader
	writeMu      sync.Mutex
	Nick         string
	HistoryLimit int
}

const readTimeout = 30 * time.Second

// Register performs CAP negotiation and registration over conn.
func Register(conn net.Conn) (*Client, error) {
	c := &Client{conn: conn, reader: bufio.NewReader(conn)}
	for _, line := range []string{
		"CAP LS 302",
		"CAP REQ :batch draft/chathistory echo-message message-tags server-time",
		"NICK tailirc",
		"USER tailirc 0 * :tailirc",
	} {
		if err := c.writeLine(line); err != nil {
			return nil, err
		}
	}
	for {
		msg, err := c.readMessage()
		if err != nil {
			return nil, fmt.Errorf("register: %w", err)
		}
		switch msg.Command {
		case "CAP":
			switch param(msg, 1) {
			case "ACK":
				if err := c.writeLine("CAP END"); err != nil {
					return nil, err
				}
			case "NAK":
				return nil, fmt.Errorf("server does not support chathistory")
			}
		case "001":
			c.Nick = param(msg, 0)
		case "005":
			for _, token := range msg.Params[1:] {
				if v, ok := strings.CutPrefix(token, "CHATHISTORY="); ok {
					c.HistoryLimit, _ = strconv.Atoi(v)
				}
			}
		case "376", "422":
			return c, nil
		case "ERROR":
			return nil, fmt.Errorf("server error: %s", param(msg, 0))
		}
	}
}

// Send posts text to target, one PRIVMSG per line.
func (c *Client) Send(target, text string) error {
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if line == "" {
			line = " "
		}
		msg := ircmsg.MakeMessage(nil, "", "PRIVMSG", target, line)
		encoded, err := msg.Line()
		if err != nil {
			return err
		}
		if err := c.writeLine(strings.TrimRight(encoded, "\r\n")); err != nil {
			return err
		}
	}
	return nil
}

// History returns the latest limit messages in target using CHATHISTORY
// LATEST. A limit of 0 means the server's advertised maximum.
func (c *Client) History(target string, limit int) ([]Message, error) {
	if limit <= 0 || (c.HistoryLimit > 0 && limit > c.HistoryLimit) {
		limit = c.HistoryLimit
	}
	if limit <= 0 {
		return nil, errors.New("server did not advertise CHATHISTORY")
	}
	if err := c.writeLine(fmt.Sprintf("CHATHISTORY LATEST %s * %d", target, limit)); err != nil {
		return nil, err
	}
	var batchID string
	var messages []Message
	for {
		msg, err := c.readMessage()
		if err != nil {
			return nil, err
		}
		switch msg.Command {
		case "BATCH":
			ref := param(msg, 0)
			if id, ok := strings.CutPrefix(ref, "+"); ok && param(msg, 1) == "chathistory" {
				batchID = id
			} else if ref == "-"+batchID && batchID != "" {
				return messages, nil
			}
		case "PRIVMSG", "NOTICE":
			if _, b := msg.GetTag("batch"); batchID == "" || b != batchID {
				continue
			}
			m := Message{Nick: msg.Nick(), Target: param(msg, 0), Text: param(msg, 1)}
			if ok, ts := msg.GetTag("time"); ok {
				m.Time, _ = time.Parse(time.RFC3339Nano, ts)
			}
			messages = append(messages, m)
		case "FAIL":
			return nil, fmt.Errorf("chathistory: %s", strings.Join(msg.Params[1:], " "))
		}
	}
}

// Quit sends QUIT and waits for the server to close the connection, so
// everything sent before it has been processed.
func (c *Client) Quit() error {
	if err := c.writeLine("QUIT"); err != nil {
		return err
	}
	for {
		if _, err := c.readMessage(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (c *Client) writeLine(line string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(readTimeout))
	_, err := io.WriteString(c.conn, line+"\r\n")
	return err
}

func (c *Client) readMessage() (ircmsg.Message, error) {
	for {
		c.conn.SetReadDeadline(time.Now().Add(readTimeout))
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

func param(msg ircmsg.Message, i int) string {
	if i < len(msg.Params) {
		return msg.Params[i]
	}
	return ""
}
