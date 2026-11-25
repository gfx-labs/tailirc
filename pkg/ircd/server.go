// Package ircd is a small in-memory IRC server with IRCv3 chathistory.
// Client identity comes from the transport, not from NICK.
package ircd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ergochat/irc-go/ircmsg"
)

const (
	ServerName       = "tailirc"
	maxLineLength    = 8192
	clientQueueDepth = 1024
	timeFormat       = "2006-01-02T15:04:05.000Z"
)

// Capabilities this server supports.
var supportedCaps = []string{
	"batch",
	"draft/chathistory",
	"echo-message",
	"message-tags",
	"server-time",
}

// Config configures a Server.
type Config struct {
	// HistoryLimit is the number of messages kept per channel. It is also the
	// CHATHISTORY limit advertised to clients, so one request can read the
	// whole buffer.
	HistoryLimit int
	// Logf logs connection events. If nil, log.Printf is used.
	Logf func(format string, args ...any)
}

// Identity says who a connection is. ID is unique per client (the key's hex
// prefix) and is the IRC username, so sources read nick!id@tailirc. Nick is
// the display name and may repeat across clients.
type Identity struct {
	Nick string
	ID   string
}

// Identify returns the identity of an accepted connection, or ok=false to
// reject it.
type Identify func(c net.Conn) (id Identity, ok bool)

// Server holds all channels and connected clients in memory.
type Server struct {
	config Config

	mu       sync.Mutex
	nextID   uint64
	channels map[string]*channel // keyed by casefolded name
	clients  map[string]*client  // keyed by casefolded ID
}

type channel struct {
	name    string
	members map[*client]bool
	history []StoredMessage
}

type client struct {
	server *Server
	conn   net.Conn
	nick   string
	id     string
	out    chan []byte
	done   chan struct{}
	once   sync.Once

	// Registration state, touched only by the reading goroutine.
	capNegotiating bool
	gotUser        bool
	registered     bool

	capsMu sync.Mutex
	caps   map[string]bool
}

// New returns a Server.
func New(config Config) *Server {
	if config.HistoryLimit <= 0 {
		config.HistoryLimit = 1000
	}
	if config.Logf == nil {
		config.Logf = log.Printf
	}
	return &Server{
		config:   config,
		channels: map[string]*channel{},
		clients:  map[string]*client{},
	}
}

// Serve accepts connections on l until ctx is done or l fails.
func (s *Server) Serve(ctx context.Context, l net.Listener, identify Identify) error {
	go func() {
		<-ctx.Done()
		l.Close()
	}()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		id, ok := identify(c)
		if !ok {
			s.config.Logf("rejecting unidentified connection from %v", c.RemoteAddr())
			c.Close()
			continue
		}
		go s.handle(c, id)
	}
}

// ServeConn serves one already identified connection, for example the host's
// own client over net.Pipe. It returns when the connection closes.
func (s *Server) ServeConn(c net.Conn, id Identity) { s.handle(c, id) }

func (s *Server) handle(conn net.Conn, id Identity) {
	cl := &client{
		server: s,
		conn:   conn,
		nick:   id.Nick,
		id:     id.ID,
		out:    make(chan []byte, clientQueueDepth),
		done:   make(chan struct{}),
		caps:   map[string]bool{},
	}
	s.config.Logf("%s connected from %v", cl.display(), conn.RemoteAddr())
	go cl.writeLoop()
	cl.readLoop()
	cl.close()
	s.removeClient(cl, "Connection closed")
	s.config.Logf("%s disconnected", cl.display())
}

func (c *client) display() string {
	if c.nick == c.id {
		return c.id
	}
	return c.nick + "!" + c.id
}

func (c *client) writeLoop() {
	w := bufio.NewWriter(c.conn)
	for {
		select {
		case line := <-c.out:
			w.Write(line)
			// Batch lines that are already queued into one write.
			for n := len(c.out); n > 0; n-- {
				w.Write(<-c.out)
			}
			if err := w.Flush(); err != nil {
				c.close()
				return
			}
		case <-c.done:
			// Flush anything queued before close, like an ERROR line.
			for n := len(c.out); n > 0; n-- {
				w.Write(<-c.out)
			}
			w.Flush()
			c.conn.Close()
			return
		}
	}
}

func (c *client) close() {
	c.once.Do(func() { close(c.done) })
}

func (c *client) hasCap(name string) bool {
	c.capsMu.Lock()
	defer c.capsMu.Unlock()
	return c.caps[name]
}

// send queues msg. A client that falls too far behind is disconnected rather
// than blocking the rest of the server.
func (c *client) send(msg ircmsg.Message) {
	if !c.hasCap("message-tags") {
		for tag := range msg.AllTags() {
			if tag != "time" && tag != "batch" {
				msg.DeleteTag(tag)
			}
		}
	}
	if !c.hasCap("server-time") {
		msg.DeleteTag("time")
	}
	if !c.hasCap("batch") {
		msg.DeleteTag("batch")
	}
	line, err := msg.LineBytesStrict(false, 0)
	if err != nil {
		c.server.config.Logf("encode %s to %s: %v", msg.Command, c.nick, err)
		return
	}
	select {
	case c.out <- line:
	case <-c.done:
	default:
		c.server.config.Logf("%s send queue full, disconnecting", c.nick)
		c.close()
	}
}

func (c *client) prefix() string {
	return c.nick + "!" + c.id + "@" + ServerName
}

func (c *client) reply(command string, params ...string) {
	c.send(ircmsg.MakeMessage(nil, ServerName, command, append([]string{c.nick}, params...)...))
}

func (c *client) readLoop() {
	r := bufio.NewReaderSize(c.conn, maxLineLength)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		msg, err := ircmsg.ParseLineStrict(line, true, maxLineLength)
		if err != nil {
			if errors.Is(err, ircmsg.ErrorLineIsEmpty) {
				continue
			}
			c.reply("417", "Input line was too long or malformed")
			continue
		}
		if quit := c.dispatch(msg); quit {
			return
		}
	}
}

func (c *client) dispatch(msg ircmsg.Message) (quit bool) {
	cmd := strings.ToUpper(msg.Command)
	switch cmd {
	case "CAP":
		c.handleCap(msg)
	case "NICK":
		// Nicks are derived from the client's key. Registration accepts any
		// NICK, and the fixed nick is announced in the welcome.
		if c.registered {
			c.reply("432", param(msg, 0), "Nick is fixed to your key")
		}
	case "USER":
		c.gotUser = true
	case "PING":
		c.send(ircmsg.MakeMessage(nil, ServerName, "PONG", ServerName, param(msg, 0)))
	case "PONG":
	case "QUIT":
		c.send(ircmsg.MakeMessage(nil, ServerName, "ERROR", "Closing link"))
		c.server.removeClient(c, cmp(param(msg, 0), "Quit"))
		return true
	default:
		if !c.registered {
			c.reply("451", "You have not registered")
			return false
		}
		c.dispatchRegistered(cmd, msg)
	}
	c.maybeRegister()
	return false
}

func (c *client) dispatchRegistered(cmd string, msg ircmsg.Message) {
	s := c.server
	switch cmd {
	case "JOIN":
		if param(msg, 0) == "0" {
			s.partAll(c)
			return
		}
		for _, name := range strings.Split(param(msg, 0), ",") {
			s.join(c, name)
		}
	case "PART":
		for _, name := range strings.Split(param(msg, 0), ",") {
			s.part(c, name, param(msg, 1))
		}
	case "PRIVMSG", "NOTICE":
		if len(msg.Params) < 2 {
			c.reply("461", cmd, "Not enough parameters")
			return
		}
		for _, target := range strings.Split(msg.Params[0], ",") {
			s.message(c, cmd, target, msg.Params[1], msg.ClientOnlyTags())
		}
	case "TAGMSG":
	case "NAMES":
		for _, name := range strings.Split(param(msg, 0), ",") {
			s.names(c, name)
		}
	case "TOPIC":
		c.reply("331", param(msg, 0), "No topic is set")
	case "MODE":
		target := param(msg, 0)
		if isChannel(target) {
			c.reply("324", target, "+")
		} else {
			c.reply("221", "+")
		}
	case "WHO":
		c.reply("315", param(msg, 0), "End of WHO list")
	case "LIST":
		s.list(c)
	case "CHATHISTORY":
		s.chathistory(c, msg)
	default:
		c.reply("421", cmd, "Unknown command")
	}
}

func (c *client) handleCap(msg ircmsg.Message) {
	sub := strings.ToUpper(param(msg, 0))
	target := "*"
	if c.registered {
		target = c.nick
	}
	switch sub {
	case "LS":
		if !c.registered {
			c.capNegotiating = true
		}
		c.send(ircmsg.MakeMessage(nil, ServerName, "CAP", target, "LS", strings.Join(supportedCaps, " ")))
	case "LIST":
		c.capsMu.Lock()
		var enabled []string
		for name := range c.caps {
			enabled = append(enabled, name)
		}
		c.capsMu.Unlock()
		slices.Sort(enabled)
		c.send(ircmsg.MakeMessage(nil, ServerName, "CAP", target, "LIST", strings.Join(enabled, " ")))
	case "REQ":
		if !c.registered {
			c.capNegotiating = true
		}
		requested := strings.Fields(param(msg, 1))
		for _, r := range requested {
			if !slices.Contains(supportedCaps, strings.TrimPrefix(r, "-")) {
				c.send(ircmsg.MakeMessage(nil, ServerName, "CAP", target, "NAK", param(msg, 1)))
				return
			}
		}
		c.capsMu.Lock()
		for _, r := range requested {
			if name, ok := strings.CutPrefix(r, "-"); ok {
				delete(c.caps, name)
			} else {
				c.caps[r] = true
			}
		}
		c.capsMu.Unlock()
		c.send(ircmsg.MakeMessage(nil, ServerName, "CAP", target, "ACK", param(msg, 1)))
	case "END":
		c.capNegotiating = false
	default:
		c.reply("410", sub, "Invalid CAP command")
	}
}

func (c *client) maybeRegister() {
	if c.registered || c.capNegotiating || !c.gotUser {
		return
	}
	if err := c.server.addClient(c); err != nil {
		c.send(ircmsg.MakeMessage(nil, ServerName, "ERROR", err.Error()))
		c.close()
		return
	}
	c.registered = true
	limit := strconv.Itoa(c.server.config.HistoryLimit)
	c.reply("001", "Welcome to tailirc, "+c.prefix())
	c.reply("002", "Your host is "+ServerName)
	c.reply("003", "This server keeps messages in memory only")
	c.reply("004", ServerName, "tailirc", "i", "n")
	c.reply("005", "CASEMAPPING=ascii", "CHANTYPES=#", "CHATHISTORY="+limit,
		"MSGREFTYPES=msgid,timestamp", "NETWORK=tailirc", "NICKLEN=64", "are supported by this server")
	c.reply("422", "MOTD File is missing")
}

func (s *Server) addClient(c *client) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fold(c.id)
	if old := s.clients[key]; old != nil {
		// Same key reconnecting: replace the stale session.
		s.removeLocked(old, "Reconnected")
		old.close()
	}
	s.clients[key] = c
	return nil
}

func (s *Server) removeClient(c *client, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients[fold(c.id)] == c {
		s.removeLocked(c, reason)
	}
}

func (s *Server) removeLocked(c *client, reason string) {
	quit := ircmsg.MakeMessage(nil, c.prefix(), "QUIT", reason)
	notified := map[*client]bool{c: true}
	for _, ch := range s.channels {
		if !ch.members[c] {
			continue
		}
		delete(ch.members, c)
		for m := range ch.members {
			if !notified[m] {
				notified[m] = true
				m.send(quit)
			}
		}
	}
	delete(s.clients, fold(c.id))
}

func (s *Server) join(c *client, name string) {
	if !validChannel(name) {
		c.reply("403", name, "No such channel")
		return
	}
	s.mu.Lock()
	ch := s.channelLocked(name)
	if ch.members[c] {
		s.mu.Unlock()
		return
	}
	ch.members[c] = true
	joinMsg := ircmsg.MakeMessage(nil, c.prefix(), "JOIN", ch.name)
	for m := range ch.members {
		m.send(joinMsg)
	}
	nicks := ch.nicks()
	s.mu.Unlock()
	c.reply("331", ch.name, "No topic is set")
	c.reply("353", "=", ch.name, strings.Join(nicks, " "))
	c.reply("366", ch.name, "End of /NAMES list")
}

func (s *Server) part(c *client, name, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.channels[fold(name)]
	if ch == nil || !ch.members[c] {
		c.reply("442", name, "You're not on that channel")
		return
	}
	partMsg := ircmsg.MakeMessage(nil, c.prefix(), "PART", ch.name, reason)
	for m := range ch.members {
		m.send(partMsg)
	}
	delete(ch.members, c)
}

func (s *Server) partAll(c *client) {
	s.mu.Lock()
	var names []string
	for _, ch := range s.channels {
		if ch.members[c] {
			names = append(names, ch.name)
		}
	}
	s.mu.Unlock()
	for _, name := range names {
		s.part(c, name, "")
	}
}

// message stores and relays a PRIVMSG or NOTICE. Channels do not require
// membership to post, so one-shot senders need not join.
func (s *Server) message(c *client, cmd, target, text string, clientTags map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	tags := map[string]string{
		"msgid": strconv.FormatUint(s.nextID, 10) + "-" + randomHex(4),
		"time":  time.Now().UTC().Format(timeFormat),
	}
	for k, v := range clientTags {
		tags[k] = v
	}
	out := ircmsg.MakeMessage(tags, c.prefix(), cmd, target, text)

	if isChannel(target) {
		if !validChannel(target) {
			c.reply("403", target, "No such channel")
			return
		}
		ch := s.channelLocked(target)
		ch.history = append(ch.history, StoredMessage{
			ID:      s.nextID,
			MsgID:   tags["msgid"],
			Time:    time.Now().UTC(),
			Source:  c.prefix(),
			Command: cmd,
			Target:  ch.name,
			Text:    text,
		})
		if over := len(ch.history) - s.config.HistoryLimit; over > 0 {
			ch.history = slices.Delete(ch.history, 0, over)
		}
		for m := range ch.members {
			if m != c || c.hasCap("echo-message") {
				m.send(out)
			}
		}
		if !ch.members[c] && c.hasCap("echo-message") {
			c.send(out)
		}
		return
	}

	dest := s.clients[fold(target)]
	if dest == nil {
		if cmd == "PRIVMSG" {
			c.reply("401", target, "No such nick")
		}
		return
	}
	dest.send(out)
	if c.hasCap("echo-message") && dest != c {
		c.send(out)
	}
}

func (s *Server) names(c *client, name string) {
	s.mu.Lock()
	ch := s.channels[fold(name)]
	var nicks []string
	if ch != nil {
		name = ch.name
		nicks = ch.nicks()
	}
	s.mu.Unlock()
	if len(nicks) > 0 {
		c.reply("353", "=", name, strings.Join(nicks, " "))
	}
	c.reply("366", name, "End of /NAMES list")
}

func (s *Server) list(c *client) {
	s.mu.Lock()
	type entry struct {
		name  string
		count int
	}
	var entries []entry
	for _, ch := range s.channels {
		entries = append(entries, entry{ch.name, len(ch.members)})
	}
	s.mu.Unlock()
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.name, b.name) })
	c.reply("321", "Channel", "Users  Name")
	for _, e := range entries {
		c.reply("322", e.name, strconv.Itoa(e.count), "")
	}
	c.reply("323", "End of /LIST")
}

// channelLocked returns the named channel, creating it. Callers hold s.mu.
func (s *Server) channelLocked(name string) *channel {
	ch := s.channels[fold(name)]
	if ch == nil {
		ch = &channel{name: name, members: map[*client]bool{}}
		s.channels[fold(name)] = ch
	}
	return ch
}

func (ch *channel) nicks() []string {
	var nicks []string
	for m := range ch.members {
		nicks = append(nicks, m.display())
	}
	slices.Sort(nicks)
	return nicks
}

func (s *Server) chathistory(c *client, msg ircmsg.Message) {
	fail := func(code, context, description string) {
		params := []string{"CHATHISTORY", code}
		if context != "" {
			params = append(params, context)
		}
		c.send(ircmsg.MakeMessage(nil, ServerName, "FAIL", append(params, description)...))
	}
	sub := strings.ToUpper(param(msg, 0))
	if sub == "TARGETS" {
		s.chathistoryTargets(c, msg, fail)
		return
	}
	target := param(msg, 1)
	if target == "" {
		fail("NEED_MORE_PARAMS", sub, "Missing parameters")
		return
	}
	if !isChannel(target) {
		fail("INVALID_TARGET", sub+" "+target, "History is only kept for channels")
		return
	}

	limitParam := msg.Params[len(msg.Params)-1]
	limit, err := strconv.Atoi(limitParam)
	if err != nil || limit <= 0 {
		fail("INVALID_PARAMS", sub, "Invalid limit")
		return
	}
	limit = min(limit, s.config.HistoryLimit)

	refs := make([]MessageRef, 0, 2)
	refCount := map[string]int{"BEFORE": 1, "AFTER": 1, "LATEST": 1, "AROUND": 1, "BETWEEN": 2}[sub]
	if refCount == 0 {
		fail("INVALID_PARAMS", sub, "Unknown subcommand")
		return
	}
	if len(msg.Params) != 3+refCount {
		fail("NEED_MORE_PARAMS", sub, "Wrong number of parameters")
		return
	}
	for _, raw := range msg.Params[2 : 2+refCount] {
		ref, err := parseMessageRef(raw)
		if err != nil || (ref.Any && sub != "LATEST") {
			fail("INVALID_MSGREFTYPE", sub+" "+raw, "Invalid message reference")
			return
		}
		refs = append(refs, ref)
	}

	s.mu.Lock()
	var selected []StoredMessage
	name := target
	if ch := s.channels[fold(target)]; ch != nil {
		name = ch.name
		for i := range refs {
			if refs[i].IsMsgID && !resolveMsgID(ch.history, &refs[i]) {
				s.mu.Unlock()
				fail("INVALID_MSGREFTYPE", sub, "Unknown msgid")
				return
			}
		}
		switch sub {
		case "BEFORE":
			selected = historyBefore(ch.history, refs[0], limit)
		case "AFTER":
			selected = historyAfter(ch.history, refs[0], limit)
		case "LATEST":
			selected = historyLatest(ch.history, refs[0], limit)
		case "AROUND":
			selected = historyAround(ch.history, refs[0], limit)
		case "BETWEEN":
			selected = historyBetween(ch.history, refs[0], refs[1], limit)
		}
		selected = slices.Clone(selected)
	}
	s.mu.Unlock()

	batchID := randomHex(6)
	c.send(ircmsg.MakeMessage(nil, ServerName, "BATCH", "+"+batchID, "chathistory", name))
	for _, m := range selected {
		tags := map[string]string{"batch": batchID, "msgid": m.MsgID, "time": m.Time.Format(timeFormat)}
		c.send(ircmsg.MakeMessage(tags, m.Source, m.Command, m.Target, m.Text))
	}
	c.send(ircmsg.MakeMessage(nil, ServerName, "BATCH", "-"+batchID))
}

func (s *Server) chathistoryTargets(c *client, msg ircmsg.Message, fail func(code, context, description string)) {
	if len(msg.Params) != 4 {
		fail("NEED_MORE_PARAMS", "TARGETS", "Wrong number of parameters")
		return
	}
	from, err1 := parseMessageRef(msg.Params[1])
	to, err2 := parseMessageRef(msg.Params[2])
	limit, err3 := strconv.Atoi(msg.Params[3])
	if err1 != nil || err2 != nil || from.IsMsgID || to.IsMsgID || from.Any || to.Any {
		fail("INVALID_MSGREFTYPE", "TARGETS", "TARGETS takes timestamps")
		return
	}
	if err3 != nil || limit <= 0 {
		fail("INVALID_PARAMS", "TARGETS", "Invalid limit")
		return
	}
	lo, hi := from.Time, to.Time
	if hi.Before(lo) {
		lo, hi = hi, lo
	}
	type target struct {
		name   string
		latest time.Time
	}
	var targets []target
	s.mu.Lock()
	for _, ch := range s.channels {
		if len(ch.history) == 0 {
			continue
		}
		latest := ch.history[len(ch.history)-1].Time
		if latest.After(lo) && latest.Before(hi) {
			targets = append(targets, target{ch.name, latest})
		}
	}
	s.mu.Unlock()
	slices.SortFunc(targets, func(a, b target) int { return a.latest.Compare(b.latest) })
	if len(targets) > limit {
		targets = targets[:limit]
	}
	batchID := randomHex(6)
	c.send(ircmsg.MakeMessage(nil, ServerName, "BATCH", "+"+batchID, "draft/chathistory-targets"))
	for _, t := range targets {
		c.send(ircmsg.MakeMessage(map[string]string{"batch": batchID}, ServerName, "CHATHISTORY", "TARGETS", t.name, t.latest.Format(timeFormat)))
	}
	c.send(ircmsg.MakeMessage(nil, ServerName, "BATCH", "-"+batchID))
}

func param(msg ircmsg.Message, i int) string {
	if i < len(msg.Params) {
		return msg.Params[i]
	}
	return ""
}

func cmp(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func isChannel(name string) bool { return strings.HasPrefix(name, "#") }

func validChannel(name string) bool {
	return len(name) > 1 && len(name) <= 64 && isChannel(name) && !strings.ContainsAny(name, " ,\x07\x00\r\n")
}

// fold casefolds with ASCII casemapping.
func fold(s string) string { return strings.ToLower(s) }

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// String implements fmt.Stringer for debug output.
func (m StoredMessage) String() string {
	return fmt.Sprintf("%s %s %s: %s", m.Time.Format(timeFormat), m.Target, m.Source, m.Text)
}
