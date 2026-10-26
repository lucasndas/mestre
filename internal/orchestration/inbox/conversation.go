package inbox

import (
	"context"
	"database/sql"
	"errors"
)

// The reads the panel makes of the conversation of a user with the Grand Master
// (docs/painel/conversa-e-aprovacoes.md#conversa-com-o-grão-mestre): what the user
// wrote, on any channel, and what the Grand Master answered. A user only ever gets
// his own: the filter is the user of the origin, never one the client says.

// UserItems lists the messages of a user to a workspace, newest first, whatever their
// channel and state.
func (s *Store) UserItems(ctx context.Context, workspace, user string, limit int) ([]Item, error) {
	return s.list(ctx, `workspace = ? AND kind = 'user' AND json_valid(origin) AND json_extract(origin, '$.user') = ? ORDER BY seq DESC LIMIT ?`,
		workspace, user, limit)
}

// UserItem is one message of a user of a workspace; ErrNoItem when it is not his.
func (s *Store) UserItem(ctx context.Context, workspace, user, id string) (Item, error) {
	it, err := s.Get(ctx, id)
	if err != nil {
		return Item{}, err
	}
	if it.Kind != KindUser || it.Workspace != workspace || it.Origin == nil || it.Origin.User != user {
		return Item{}, ErrNoItem
	}
	return it, nil
}

// OutboundOf lists the answers to a user in a workspace, newest first, whatever
// their channel and state.
func (s *Store) OutboundOf(ctx context.Context, workspace, user string, limit int) ([]Outbound, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+outColumns+` FROM outbound_messages WHERE workspace = ? AND user_id = ? ORDER BY id DESC LIMIT ?`, workspace, user, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Outbound
	for rows.Next() {
		o, err := scanOut(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OutboundGet reads an answer; ErrNoItem when there is none.
func (s *Store) OutboundGet(ctx context.Context, id int64) (Outbound, error) {
	o, err := scanOut(s.DB.Reader().QueryRowContext(ctx, `SELECT `+outColumns+` FROM outbound_messages WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Outbound{}, ErrNoItem
	}
	return o, err
}
