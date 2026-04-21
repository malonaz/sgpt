package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/malonaz/core/go/postgres"

	"github.com/malonaz/sgpt/sgpt/model"
)

func (s *Store) UpdateChatSearchableContent(ctx context.Context, chatId string, searchableContent string) (bool, error) {
	query := `UPDATE chat SET searchable_content = $2 WHERE chat_id = $1`
	result, err := s.client.Exec(ctx, query, chatId, searchableContent)
	if err != nil {
		return false, err
	}
	return result.RowsAffected() == 1, nil
}

func (s *Store) SearchChats(ctx context.Context, searchQuery, whereClause, paginationClause string, columns []string, params ...any) ([]*model.Chat, error) {
	if columns == nil {
		columns = ChatPostgresColumns
	}

	whereClause = postgres.AddToWhereClause(whereClause, "delete_time IS NULL")

	whereClause = postgres.AddToWhereClause(whereClause, fmt.Sprintf("searchable_content ILIKE $%d", len(params)+1))
	params = append(params, "%"+searchQuery+"%")

	query := fmt.Sprintf("SELECT %%s FROM chat %s %s", whereClause, paginationClause)
	query = postgres.SelectQuery(query, columns)

	var chats []*model.Chat
	transactionFN := func(tx postgres.Tx) error {
		chats = nil
		rows, err := tx.Query(ctx, query, params...)
		if err != nil {
			if err == pgx.ErrNoRows {
				return nil
			}
			return fmt.Errorf("searching chats: %w", err)
		}
		chats, err = pgx.CollectRows(rows, pgx.RowToAddrOfStructByNameLax[model.Chat])
		if err != nil {
			return fmt.Errorf("collecting rows: %w", err)
		}
		return nil
	}
	return chats, s.client.ExecuteTransaction(ctx, postgres.RepeatableRead, transactionFN)
}
