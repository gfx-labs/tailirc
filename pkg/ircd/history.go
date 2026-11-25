package ircd

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// StoredMessage is a channel message kept in memory for CHATHISTORY.
type StoredMessage struct {
	ID      uint64 // server-wide sequence, increasing with Time
	MsgID   string
	Time    time.Time
	Source  string
	Command string
	Target  string
	Text    string
}

// MessageRef is a CHATHISTORY reference: "*", "timestamp=..." or "msgid=...".
// A msgid reference is resolved to the message's ID before use.
type MessageRef struct {
	Any     bool
	IsMsgID bool
	MsgID   string
	ID      uint64
	Time    time.Time
}

func parseMessageRef(s string) (MessageRef, error) {
	if s == "*" {
		return MessageRef{Any: true}, nil
	}
	kind, value, ok := strings.Cut(s, "=")
	if !ok || value == "" {
		return MessageRef{}, fmt.Errorf("invalid reference %q", s)
	}
	switch kind {
	case "timestamp":
		t, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return MessageRef{}, fmt.Errorf("invalid timestamp %q", value)
		}
		return MessageRef{Time: t}, nil
	case "msgid":
		return MessageRef{IsMsgID: true, MsgID: value}, nil
	}
	return MessageRef{}, fmt.Errorf("unsupported reference type %q", kind)
}

// resolveMsgID fills in ref's ID and time from history.
func resolveMsgID(history []StoredMessage, ref *MessageRef) bool {
	for _, m := range history {
		if m.MsgID == ref.MsgID {
			ref.ID, ref.Time = m.ID, m.Time
			return true
		}
	}
	return false
}

// lowerIndex returns the index of the first message at or after ref.
func lowerIndex(history []StoredMessage, ref MessageRef) int {
	return sort.Search(len(history), func(i int) bool {
		if ref.IsMsgID {
			return history[i].ID >= ref.ID
		}
		return !history[i].Time.Before(ref.Time)
	})
}

// upperIndex returns the index of the first message strictly after ref.
func upperIndex(history []StoredMessage, ref MessageRef) int {
	return sort.Search(len(history), func(i int) bool {
		if ref.IsMsgID {
			return history[i].ID > ref.ID
		}
		return history[i].Time.After(ref.Time)
	})
}

func lastN(history []StoredMessage, n int) []StoredMessage {
	if len(history) > n {
		return history[len(history)-n:]
	}
	return history
}

func firstN(history []StoredMessage, n int) []StoredMessage {
	if len(history) > n {
		return history[:n]
	}
	return history
}

func historyBefore(history []StoredMessage, ref MessageRef, limit int) []StoredMessage {
	return lastN(history[:lowerIndex(history, ref)], limit)
}

func historyAfter(history []StoredMessage, ref MessageRef, limit int) []StoredMessage {
	return firstN(history[upperIndex(history, ref):], limit)
}

func historyLatest(history []StoredMessage, ref MessageRef, limit int) []StoredMessage {
	if ref.Any {
		return lastN(history, limit)
	}
	return lastN(history[upperIndex(history, ref):], limit)
}

func historyAround(history []StoredMessage, ref MessageRef, limit int) []StoredMessage {
	start := max(lowerIndex(history, ref)-limit/2, 0)
	end := min(start+limit, len(history))
	return history[start:end]
}

// historyBetween returns messages strictly between the two references. When
// the first reference is later, the newest messages are kept, per the spec.
func historyBetween(history []StoredMessage, from, to MessageRef, limit int) []StoredMessage {
	reversed := to.Time.Before(from.Time) || (from.IsMsgID && to.IsMsgID && to.ID < from.ID)
	if reversed {
		from, to = to, from
	}
	lo, hi := upperIndex(history, from), lowerIndex(history, to)
	if lo >= hi {
		return nil
	}
	if reversed {
		return lastN(history[lo:hi], limit)
	}
	return firstN(history[lo:hi], limit)
}
