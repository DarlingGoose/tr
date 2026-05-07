package textractor

import (
	"context"
	"sort"
)

type hookSubscriber struct {
	group string
	ch    chan *Line
}

func (c *Client) HookGroups() []string {
	c.hookMu.RLock()
	defer c.hookMu.RUnlock()

	groups := make([]string, 0, len(c.hookHistory))
	for group := range c.hookHistory {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

func (c *Client) HookHistory(group string) []*Line {
	group = HookGroup(group)

	c.hookMu.RLock()
	defer c.hookMu.RUnlock()

	lines := c.hookHistory[group]
	history := cloneLines(lines)
	return history
}

func (c *Client) HookHistories() map[string][]*Line {
	c.hookMu.RLock()
	defer c.hookMu.RUnlock()

	histories := make(map[string][]*Line, len(c.hookHistory))
	for group, lines := range c.hookHistory {
		histories[group] = cloneLines(lines)
	}
	return histories
}

func (c *Client) HookFeed(ctx context.Context, group string, replayHistory bool) <-chan *Line {
	group = HookGroup(group)
	out := make(chan *Line, 256)

	c.hookMu.Lock()
	if replayHistory {
		c.replayHookHistoryLocked(out, group)
	}

	id := c.nextHookSubID
	c.nextHookSubID++
	c.hookSubscribers[id] = hookSubscriber{
		group: group,
		ch:    out,
	}
	c.hookMu.Unlock()

	go func() {
		<-ctx.Done()

		c.hookMu.Lock()
		delete(c.hookSubscribers, id)
		close(out)
		c.hookMu.Unlock()
	}()

	return out
}

func (c *Client) replayHookHistoryLocked(out chan<- *Line, group string) {
	if group != "" {
	replayGroup:
		for _, line := range c.hookHistory[group] {
			select {
			case out <- line.Clone():
			default:
				break replayGroup
			}
		}
		return
	}

	groups := make([]string, 0, len(c.hookHistory))
	for group := range c.hookHistory {
		groups = append(groups, group)
	}
	sort.Strings(groups)

replayAll:
	for _, group := range groups {
		for _, line := range c.hookHistory[group] {
			select {
			case out <- line.Clone():
			default:
				break replayAll
			}
		}
	}
}

func (c *Client) recordHookLine(line *Line) {
	if line == nil {
		return
	}

	stored := line.Clone()
	group := stored.HookGroup()
	if group == "" {
		return
	}

	c.hookMu.Lock()
	defer c.hookMu.Unlock()

	c.hooks[stored.Hook] = stored.Text

	history := append(c.hookHistory[group], stored)
	if len(history) > c.hookHistoryLimit {
		copy(history, history[len(history)-c.hookHistoryLimit:])
		history = history[:c.hookHistoryLimit]
	}
	c.hookHistory[group] = history

	if c.hookHistoryLog != nil {
		c.hookHistoryLog.Enqueue(group, stored)
	}

	for _, sub := range c.hookSubscribers {
		if sub.group != "" && sub.group != group {
			continue
		}

		select {
		case sub.ch <- stored.Clone():
		default:
			if c.Logger != nil {
				c.Logger.Warn("dropping hook feed line; channel full", "hook_group", group)
			}
		}
	}
}

func cloneLines(lines []*Line) []*Line {
	history := make([]*Line, 0, len(lines))
	for _, line := range lines {
		history = append(history, line.Clone())
	}
	return history
}
